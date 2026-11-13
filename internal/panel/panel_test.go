package panel

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/index"
	"github.com/jmoruno/plexmor/internal/plex"
	"github.com/jmoruno/plexmor/internal/plextest"
	"github.com/jmoruno/plexmor/internal/servers"
)

type env struct {
	tv    *plextest.PlexTV
	accs  *accounts.Store
	reg   *servers.Registry
	panel *Panel
	h     http.Handler
}

func newEnv(t *testing.T) *env {
	t.Helper()
	casa := plex.Resource{Name: "Casa", ClientIdentifier: "casa-id", Provides: "server", Owned: true, AccessToken: "casa-owner"}
	shared := casa
	shared.Owned, shared.AccessToken, shared.SourceTitle = false, "casa-pepe", "ana"
	tv := (&plextest.PlexTV{Token: "owner-token", Resources: []plex.Resource{casa}, Accounts: map[string]plextest.Account{
		"pepe-token":     {User: plex.User{UUID: "uuid-pepe", Username: "pepe"}, Resources: []plex.Resource{shared}},
		"stranger-token": {User: plex.User{UUID: "uuid-stranger", Username: "extraño"}},
	}}).Start()
	t.Cleanup(tv.Close)
	accs, err := accounts.Open(filepath.Join(t.TempDir(), "a.json"), []byte("s"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := plex.NewClient("panel-test", "test")
	reg := servers.NewRegistry(client, tv.URL(), func() []servers.Credential {
		var out []servers.Credential
		for _, a := range accs.All() {
			out = append(out, servers.Credential{AccountID: a.ID, Token: a.PlexToken})
		}
		return out
	}, nil, time.Second, log)
	p := &Panel{Accounts: accs, Registry: reg, Store: index.NewStore(), Client: client, PlexTV: tv.URL(), Version: "v1", Log: log,
		Languages: []string{"castellano"}, OnAccountsChanged: func() { _ = reg.Refresh(context.Background()) }}
	mux := http.NewServeMux()
	p.Routes(mux)
	return &env{tv: tv, accs: accs, reg: reg, panel: p, h: mux}
}

func (e *env) do(t *testing.T, method, path string, body url.Values, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(body.Encode())
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = "plexmor.example.com"
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// login runs the PIN flow as the owner of token and returns the session cookie.
func (e *env) login(t *testing.T, token string) (*http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/login/start", nil, nil, "https://plexmor.example.com")
	var start struct{ URL string }
	if err := json.Unmarshal(rec.Body.Bytes(), &start); err != nil || !strings.HasPrefix(start.URL, "https://app.plex.tv/auth#?") {
		t.Fatalf("login start: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), `"id"`) {
		t.Fatalf("the PIN id must not reach the browser: %s", rec.Body)
	}
	browser := cookieOf(rec, loginCookie)
	if browser == nil || !browser.HttpOnly {
		t.Fatalf("login start must tie the sign-in to the browser: %v", rec.Result().Cookies())
	}
	if rec := e.do(t, http.MethodGet, "/login/check", nil, browser, ""); !strings.Contains(rec.Body.String(), "pending") {
		t.Fatalf("before approving the PIN: %s", rec.Body)
	}
	e.tv.Approve(e.tv.LastPin(), token)
	rec = e.do(t, http.MethodGet, "/login/check", nil, browser, "")
	return cookieOf(rec, cookieName), rec
}

func cookieOf(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestFirstSignInBecomesAdminThenOnlyFriendsJoin(t *testing.T) {
	e := newEnv(t)
	if rec := e.do(t, http.MethodGet, "/", nil, nil, ""); !strings.Contains(rec.Body.String(), "Entrar con Plex") || !strings.Contains(rec.Body.String(), "administrador") {
		t.Fatalf("first run login page: %s", rec.Body)
	}
	owner, rec := e.login(t, "owner-token")
	if owner == nil {
		t.Fatalf("owner login failed: %s", rec.Body)
	}
	admin := e.accs.ByUUID("uuid-owner-token")
	if admin == nil || !admin.Admin || admin.PlexToken != "owner-token" {
		t.Fatalf("the first account must be the administrator: %+v", admin)
	}
	home := e.do(t, http.MethodGet, "/", nil, owner, "").Body.String()
	if !strings.Contains(home, "http://plexmor.example.com/"+admin.ID+"/manifest.json") || !strings.Contains(home, "Usuarios") {
		t.Fatalf("dashboard should show the install URL and the admin section:\n%s", home)
	}

	pepe, _ := e.login(t, "pepe-token")
	if pepe == nil {
		t.Fatal("a friend with access to the admin's server must be able to join")
	}
	if a := e.accs.ByUUID("uuid-pepe"); a == nil || a.Admin || a.ID == admin.ID {
		t.Fatalf("friend account: %+v", a)
	}
	if c, rec := e.login(t, "stranger-token"); c != nil || rec.Code != http.StatusForbidden {
		t.Fatalf("someone without access must be refused: %d %s", rec.Code, rec.Body)
	}

	// Signing in again keeps the same account (and URL).
	again, _ := e.login(t, "owner-token")
	if again == nil || len(e.accs.All()) != 2 || e.accs.ByUUID("uuid-owner-token").ID != admin.ID {
		t.Fatalf("signing in again must reuse the account: %d accounts", len(e.accs.All()))
	}
}

func TestAdminCreatedWithoutUUIDIsRecognized(t *testing.T) {
	e := newEnv(t)
	// Created at startup from Preferences.xml while plex.tv was down.
	_ = e.accs.Put(&accounts.Account{ID: "legacy-key", PlexToken: "owner-token", Admin: true})
	_ = e.reg.Refresh(context.Background())
	c, rec := e.login(t, "owner-token")
	if c == nil {
		t.Fatalf("login: %s", rec.Body)
	}
	if len(e.accs.All()) != 1 || e.accs.Get("legacy-key").PlexUUID != "uuid-owner-token" {
		t.Fatalf("the existing administrator must be reused, not duplicated: %d accounts", len(e.accs.All()))
	}
}

func TestPrefsAndCrossSitePosts(t *testing.T) {
	e := newEnv(t)
	owner, _ := e.login(t, "owner-token")
	form := url.Values{"languages": {"Inglés, castellano"}, "max_streams": {"5"}, "mode": {"proxy"}, "res_1080": {"on"}, "transcode": {"on"}, "catalogs": {"on"}, "server_casa-id": {"on"}}
	if rec := e.do(t, http.MethodPost, "/api/prefs", form, owner, "https://evil.example.com"); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site post must be refused: %d", rec.Code)
	}
	if rec := e.do(t, http.MethodPost, "/api/prefs", form, nil, "https://plexmor.example.com"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("post without session: %d", rec.Code)
	}
	if rec := e.do(t, http.MethodPost, "/api/prefs", form, owner, "https://plexmor.example.com"); rec.Code != http.StatusOK {
		t.Fatalf("save prefs: %d %s", rec.Code, rec.Body)
	}
	p := e.accs.ByUUID("uuid-owner-token").Prefs
	if strings.Join(p.Languages, ",") != "ingles,castellano" || p.MaxStreams != 5 || p.StreamMode != accounts.ModeProxy ||
		strings.Join(p.Resolutions, ",") != "1080" || !p.Transcode || p.HideCatalogs || len(p.ExcludedServers) != 0 {
		t.Fatalf("prefs: %+v", p)
	}
	tampered := *owner
	tampered.Value = strings.Replace(owner.Value, ".", "x.", 1)
	if rec := e.do(t, http.MethodGet, "/", nil, &tampered, ""); !strings.Contains(rec.Body.String(), "Entrar con Plex") {
		t.Fatal("a tampered session must show the login page")
	}
}

func TestOnlyAdminsManageUsers(t *testing.T) {
	e := newEnv(t)
	owner, _ := e.login(t, "owner-token")
	pepe, _ := e.login(t, "pepe-token")
	pepeID := e.accs.ByUUID("uuid-pepe").ID
	if rec := e.do(t, http.MethodPost, "/api/users/"+pepeID+"/delete", nil, pepe, "https://plexmor.example.com"); rec.Code != http.StatusForbidden {
		t.Fatalf("a regular user cannot manage users: %d", rec.Code)
	}
	if rec := e.do(t, http.MethodPost, "/api/users/"+pepeID+"/delete", nil, owner, "https://plexmor.example.com"); rec.Code != http.StatusOK || e.accs.Get(pepeID) != nil {
		t.Fatalf("admin delete: %d", rec.Code)
	}
}

func TestNewURLKeepsTheAccount(t *testing.T) {
	e := newEnv(t)
	owner, _ := e.login(t, "owner-token")
	old := e.accs.ByUUID("uuid-owner-token").ID
	rec := e.do(t, http.MethodPost, "/api/newurl", nil, owner, "https://plexmor.example.com")
	now := e.accs.ByUUID("uuid-owner-token")
	if rec.Code != http.StatusOK || now == nil || now.ID == old || e.accs.Get(old) != nil || !now.Admin {
		t.Fatalf("new URL: %d %+v", rec.Code, now)
	}
}

func TestAnotherBrowserCannotFinishASignIn(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, http.MethodPost, "/login/start", nil, nil, "https://plexmor.example.com")
	if cookieOf(rec, loginCookie) == nil {
		t.Fatal("no login cookie")
	}
	e.tv.Approve(e.tv.LastPin(), "owner-token")
	// An attacker polling without the victim's cookie, or with a made-up one, gets nothing.
	for _, c := range []*http.Cookie{nil, {Name: loginCookie, Value: "guess"}} {
		rec := e.do(t, http.MethodGet, "/login/check", nil, c, "")
		if rec.Code != http.StatusNotFound || cookieOf(rec, cookieName) != nil {
			t.Fatalf("a sign-in must only finish in the browser that started it: %d %s", rec.Code, rec.Body)
		}
	}
}

func TestPendingSignInsAreCapped(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < maxPending; i++ {
		if rec := e.do(t, http.MethodPost, "/login/start", nil, nil, "https://plexmor.example.com"); rec.Code != http.StatusOK {
			t.Fatalf("start %d: %d", i, rec.Code)
		}
	}
	if rec := e.do(t, http.MethodPost, "/login/start", nil, nil, "https://plexmor.example.com"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("too many pending sign-ins must be refused: %d", rec.Code)
	}
}

func TestEnglish(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, "Sign in with Plex") || !strings.Contains(body, `lang="en"`) {
		t.Fatalf("an English browser should get the English login page:\n%s", body)
	}

	owner, _ := e.login(t, "owner-token")
	home := e.do(t, http.MethodGet, "/", nil, owner, "").Body.String()
	if !strings.Contains(home, "Salir") {
		t.Fatal("without a choice the server default (Spanish) is used")
	}
	rec = e.do(t, http.MethodPost, "/lang", url.Values{"lang": {"en"}}, owner, "https://plexmor.example.com")
	if rec.Code != http.StatusOK || cookieOf(rec, langCookie) == nil {
		t.Fatalf("switch language: %d %s", rec.Code, rec.Body)
	}
	if got := e.accs.ByUUID("uuid-owner-token").Prefs.UILanguage; got != "en" {
		t.Fatalf("the choice must be kept in the account for the texts in Stremio, got %q", got)
	}
	home = e.do(t, http.MethodGet, "/", nil, owner, "").Body.String()
	for _, want := range []string{"Sign out", "Audio languages", "Spanish (Spain)", "Español"} {
		if !strings.Contains(home, want) {
			t.Errorf("English dashboard is missing %q", want)
		}
	}
	if rec := e.do(t, http.MethodPost, "/api/prefs", url.Values{}, nil, "https://evil.example.com"); !strings.Contains(rec.Body.String(), "origin not allowed") && !strings.Contains(rec.Body.String(), "origen no permitido") {
		t.Fatalf("errors are translated: %s", rec.Body)
	}
	if rec := e.do(t, http.MethodPost, "/lang", url.Values{"lang": {"fr"}}, owner, "https://plexmor.example.com"); rec.Code != http.StatusBadRequest {
		t.Fatalf("unsupported language: %d", rec.Code)
	}
}

func TestSecurityHeadersAndCookies(t *testing.T) {
	e := newEnv(t)
	e.panel.PublicURL = "https://plexmor.example.com"
	owner, _ := e.login(t, "owner-token")
	if owner == nil || !owner.Secure || !owner.HttpOnly || owner.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie flags: %+v", owner)
	}
	rec := e.do(t, http.MethodGet, "/", nil, owner, "")
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'nonce-") || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP: %q", csp)
	}
	nonce := strings.SplitN(strings.SplitN(csp, "'nonce-", 2)[1], "'", 2)[0]
	if !strings.Contains(rec.Body.String(), `<script nonce="`+nonce+`">`) {
		t.Fatal("the page script must carry the CSP nonce")
	}
	if strings.Contains(rec.Body.String(), "fonts.googleapis.com") {
		t.Fatal("fonts are served by Plexmor, not by third parties")
	}
	again := e.do(t, http.MethodPost, "/api/newurl", nil, owner, "https://plexmor.example.com")
	if c := cookieOf(again, cookieName); c == nil || !c.Secure {
		t.Fatalf("the new session after a new URL must be Secure too: %+v", c)
	}
	// Fetch Metadata: a cross-site request is refused even without Origin.
	req := httptest.NewRequest(http.MethodPost, "/api/reindex", nil)
	req.Host = "plexmor.example.com"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(cookieOf(again, cookieName))
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site fetch: %d", w.Code)
	}
}

func TestIsAdmin(t *testing.T) {
	e := newEnv(t)
	owner, _ := e.login(t, "owner-token")
	pepe, _ := e.login(t, "pepe-token")
	for c, want := range map[*http.Cookie]bool{owner: true, pepe: false} {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.AddCookie(c)
		if e.panel.IsAdmin(req) != want {
			t.Errorf("IsAdmin = %v for %s", !want, c.Value[:8])
		}
	}
	if e.panel.IsAdmin(httptest.NewRequest(http.MethodGet, "/metrics", nil)) {
		t.Error("anonymous requests are not admins")
	}
}
