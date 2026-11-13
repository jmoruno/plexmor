// Package panel is Plexmor's web panel: sign in with Plex, install the addon, preferences,
// servers and libraries, users and status.
package panel

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/i18n"
	"github.com/jmoruno/plexmor/internal/index"
	"github.com/jmoruno/plexmor/internal/plex"
	"github.com/jmoruno/plexmor/internal/servers"
	"github.com/jmoruno/plexmor/internal/stremio"
	"github.com/jmoruno/plexmor/internal/trakt"
)

//go:embed static templates/*
var files embed.FS

const (
	cookieName  = "plexmor_session"
	loginCookie = "plexmor_login" // ties a PIN sign-in to the browser that started it
	langCookie  = "plexmor_lang"

	sessionTTL   = 30 * 24 * time.Hour
	pinTTL       = 15 * time.Minute
	maxPending   = 64       // PIN sign-ins in progress at once
	maxFormBytes = 64 << 10 // panel requests are small forms
)

// Panel serves the web panel.
type Panel struct {
	Accounts  *accounts.Store
	Registry  *servers.Registry
	Store     *index.Store
	Client    *plex.Client
	PlexTV    string
	Trakt     *trakt.Service // nil when Trakt is not configured
	Version   string
	PublicURL string
	Languages []string // audio defaults shown to accounts without preferences
	Language  string   // default interface language: i18n.ES or i18n.EN
	Log       *slog.Logger
	// OnAccountsChanged is called when someone signs in for the first time (to discover their servers).
	OnAccountsChanged func()
	// Reindex asks for an immediate sync of every server.
	Reindex func()

	tmpl   map[string]*template.Template // by interface language
	mu     sync.Mutex
	logins map[string]pending // PIN sign-ins started from this panel, by their browser cookie
}

type pending struct {
	pin     int64
	started time.Time
}

// Routes registers the panel routes.
func (p *Panel) Routes(mux *http.ServeMux) {
	base := template.Must(template.New("").Funcs(funcs(i18n.ES)).ParseFS(files, "templates/*.html"))
	p.tmpl = map[string]*template.Template{}
	for _, lang := range []string{i18n.ES, i18n.EN} {
		t := template.Must(base.Clone())
		p.tmpl[lang] = t.Funcs(funcs(lang))
	}
	p.logins = map[string]pending{}
	mux.HandleFunc("GET /{$}", p.home)
	mux.HandleFunc("GET /static/logo.png", p.static("static/logo.png", "image/png"))
	mux.HandleFunc("GET /static/logo.svg", p.static("static/logo.svg", "image/svg+xml"))
	mux.HandleFunc("GET /static/fonts/bricolage-grotesque.woff2", p.static("static/fonts/bricolage-grotesque.woff2", "font/woff2"))
	mux.HandleFunc("GET /static/fonts/instrument-sans.woff2", p.static("static/fonts/instrument-sans.woff2", "font/woff2"))
	mux.HandleFunc("POST /lang", p.sameOrigin(p.setLang))
	mux.HandleFunc("POST /login/start", p.sameOrigin(p.loginStart))
	mux.HandleFunc("GET /login/check", p.loginCheck)
	mux.HandleFunc("POST /logout", p.sameOrigin(p.logout))
	mux.HandleFunc("POST /api/prefs", p.sameOrigin(p.auth(p.savePrefs)))
	mux.HandleFunc("POST /api/newurl", p.sameOrigin(p.auth(p.newURL)))
	mux.HandleFunc("POST /api/reindex", p.sameOrigin(p.admin(p.reindex)))
	mux.HandleFunc("POST /api/users/{id}/delete", p.sameOrigin(p.admin(p.deleteUser)))
	mux.HandleFunc("POST /api/trakt/start", p.sameOrigin(p.auth(p.traktStart)))
	mux.HandleFunc("POST /api/trakt/poll", p.sameOrigin(p.auth(p.traktPoll)))
	mux.HandleFunc("POST /api/trakt/unlink", p.sameOrigin(p.auth(p.traktUnlink)))
}

// funcs are the template functions of one interface language.
func funcs(lang string) template.FuncMap {
	return template.FuncMap{
		"t":    func(key string, args ...any) string { return i18n.T(lang, key, args...) },
		"join": strings.Join,
		"initial": func(s string) string {
			for _, r := range s {
				return strings.ToUpper(string(r))
			}
			return "?"
		},
	}
}

func (p *Panel) static(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		b, err := files.ReadFile(name)
		if err != nil {
			http.NotFound(w, nil)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(b)
	}
}

// lang is the interface language of a request: the account's choice, then the language
// cookie, then the browser's languages, then the server default.
func (p *Panel) lang(r *http.Request, a *accounts.Account) string {
	if a != nil {
		if l := i18n.Parse(a.Prefs.UILanguage); l != "" {
			return l
		}
	}
	if c, err := r.Cookie(langCookie); err == nil {
		if l := i18n.Parse(c.Value); l != "" {
			return l
		}
	}
	if l := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language")); l != "" {
		return l
	}
	return i18n.Or(p.Language, i18n.ES)
}

// secure reports whether the browser reaches Plexmor over HTTPS (cookies get the Secure flag).
func (p *Panel) secure(r *http.Request) bool {
	return strings.HasPrefix(stremio.BaseURL(p.PublicURL, r), "https://")
}

func (p *Panel) setCookie(w http.ResponseWriter, r *http.Request, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: p.secure(r),
	})
}

func (p *Panel) setSession(w http.ResponseWriter, r *http.Request, accountID string) {
	p.setCookie(w, r, cookieName, p.Accounts.Session(accountID, sessionTTL), "/", int(sessionTTL.Seconds()))
}

// sameOrigin rejects cross-site requests (the session cookie is also SameSite=Lax) and caps
// the size of what is posted.
func (p *Panel) sameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			jsonError(w, http.StatusForbidden, i18n.T(p.lang(r, nil), "err.origin"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		next(w, r)
	}
}

// sameOrigin follows the browsers' Fetch Metadata first and then Origin/Referer. Requests
// with none of them do not come from a browser, so they carry no ambient cookies to abuse.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
	default:
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return err == nil && u.Host == host
}

func (p *Panel) current(r *http.Request) *accounts.Account {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil
	}
	return p.Accounts.SessionAccount(c.Value)
}

// IsAdmin reports whether the request comes from an administrator signed in to the panel.
func (p *Panel) IsAdmin(r *http.Request) bool {
	a := p.current(r)
	return a != nil && a.Admin
}

func (p *Panel) auth(next func(http.ResponseWriter, *http.Request, *accounts.Account)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := p.current(r)
		if a == nil {
			jsonError(w, http.StatusUnauthorized, i18n.T(p.lang(r, nil), "err.session"))
			return
		}
		next(w, r, a)
	}
}

func (p *Panel) admin(next func(http.ResponseWriter, *http.Request, *accounts.Account)) http.HandlerFunc {
	return p.auth(func(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
		if !a.Admin {
			jsonError(w, http.StatusForbidden, i18n.T(p.lang(r, a), "err.admin"))
			return
		}
		next(w, r, a)
	})
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// setLang changes the interface language; for a signed-in user it also changes the texts in Stremio.
func (p *Panel) setLang(w http.ResponseWriter, r *http.Request) {
	lang := i18n.Parse(r.FormValue("lang"))
	if lang == "" {
		jsonError(w, http.StatusBadRequest, i18n.T(p.lang(r, nil), "err.form"))
		return
	}
	p.setCookie(w, r, langCookie, lang, "/", 365*24*3600)
	if a := p.current(r); a != nil {
		if _, err := p.Accounts.Update(a.ID, func(a *accounts.Account) { a.Prefs.UILanguage = lang }); err != nil {
			jsonError(w, http.StatusInternalServerError, i18n.T(lang, "err.save"))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- Sign in with Plex (PIN) ----

func (p *Panel) loginStart(w http.ResponseWriter, r *http.Request) {
	lang := p.lang(r, nil)
	p.mu.Lock()
	for id, l := range p.logins {
		if time.Since(l.started) > pinTTL {
			delete(p.logins, id)
		}
	}
	busy := len(p.logins) >= maxPending
	p.mu.Unlock()
	if busy {
		jsonError(w, http.StatusTooManyRequests, i18n.T(lang, "err.busy"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	pin, err := p.Client.CreatePin(ctx, p.PlexTV)
	if err != nil {
		p.Log.Warn("panel: could not create a Plex PIN", "error", err)
		jsonError(w, http.StatusBadGateway, i18n.T(lang, "err.plextv"))
		return
	}
	// The PIN id is not secret (plex.tv numbers them in order), so the browser gets a random
	// id of its own: only the browser that started the sign-in can finish it.
	id := accounts.RandomID(16)
	p.mu.Lock()
	p.logins[id] = pending{pin: pin.ID, started: time.Now()}
	p.mu.Unlock()
	p.setCookie(w, r, loginCookie, id, "/login/", int(pinTTL.Seconds()))
	writeJSON(w, http.StatusOK, map[string]any{"url": p.Client.AuthURL(pin.Code, "")})
}

func (p *Panel) loginCheck(w http.ResponseWriter, r *http.Request) {
	lang := p.lang(r, nil)
	var id string
	if c, err := r.Cookie(loginCookie); err == nil {
		id = c.Value
	}
	p.mu.Lock()
	l, known := p.logins[id]
	p.mu.Unlock()
	if !known || time.Since(l.started) > pinTTL {
		jsonError(w, http.StatusNotFound, i18n.T(lang, "err.expired"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	pin, err := p.Client.CheckPin(ctx, p.PlexTV, l.pin)
	if err != nil {
		jsonError(w, http.StatusBadGateway, i18n.T(lang, "err.plextv"))
		return
	}
	if pin.AuthToken == "" {
		writeJSON(w, http.StatusOK, map[string]any{"pending": true})
		return
	}
	p.mu.Lock()
	delete(p.logins, id)
	p.mu.Unlock()
	p.setCookie(w, r, loginCookie, "", "/login/", -1)
	a, err := p.signIn(ctx, pin.AuthToken, lang)
	if err != nil {
		key := "err.account"
		if errors.Is(err, errNoAccess) {
			key = "err.noaccess"
		}
		jsonError(w, http.StatusForbidden, i18n.T(lang, key))
		return
	}
	p.setSession(w, r, a.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// errNoAccess is for Plex users who do not share a server with an administrator.
var errNoAccess = errors.New("no access to any server of this Plexmor")

// signIn creates or updates the account of a Plex user. The first user becomes the
// administrator; afterwards only people with access to an administrator's server may join.
// New accounts keep lang as the language of their texts.
func (p *Panel) signIn(ctx context.Context, token, lang string) (*accounts.Account, error) {
	user, err := p.Client.User(ctx, p.PlexTV, token)
	if err != nil {
		return nil, err
	}
	existing := p.Accounts.ByUUID(user.UUID)
	if existing == nil {
		// Accounts created while plex.tv was down have no UUID yet: identify them now.
		for _, a := range p.Accounts.All() {
			if a.PlexUUID == "" && a.PlexToken != "" {
				if u, err := p.Client.User(ctx, p.PlexTV, a.PlexToken); err == nil && u.UUID == user.UUID {
					existing = a
					break
				}
			}
		}
	}
	if existing != nil {
		return p.Accounts.Update(existing.ID, func(a *accounts.Account) {
			a.PlexUUID, a.PlexToken, a.Username, a.Thumb, a.LastSeen = user.UUID, token, user.Name(), user.Thumb, time.Now().Unix()
		})
	}
	admin := len(p.adminIDs()) == 0
	if !admin && !p.canJoin(ctx, token) {
		return nil, errNoAccess
	}
	a := &accounts.Account{
		ID: accounts.RandomID(16), PlexUUID: user.UUID, Username: user.Name(), Thumb: user.Thumb,
		PlexToken: token, Admin: admin, Created: time.Now().Unix(), LastSeen: time.Now().Unix(),
		Prefs: accounts.Prefs{UILanguage: lang},
	}
	if err := p.Accounts.Put(a); err != nil {
		return nil, err
	}
	p.Log.Info("panel: new account", "user", a.Username, "admin", a.Admin)
	if p.OnAccountsChanged != nil {
		p.OnAccountsChanged()
	}
	return a, nil
}

func (p *Panel) adminIDs() map[string]bool {
	ids := map[string]bool{}
	for _, a := range p.Accounts.All() {
		if a.Admin {
			ids[a.ID] = true
		}
	}
	return ids
}

// canJoin reports whether the token can reach a server owned by an administrator.
func (p *Panel) canJoin(ctx context.Context, token string) bool {
	admins := p.adminIDs()
	owned := map[string]bool{}
	for _, s := range p.Registry.All() {
		if admins[s.OwnerAccount] {
			owned[s.MachineID] = true
		}
	}
	res, err := p.Client.Resources(ctx, p.PlexTV, token)
	if err != nil {
		return false
	}
	for _, r := range res {
		if owned[r.ClientIdentifier] {
			return true
		}
	}
	return false
}

func (p *Panel) logout(w http.ResponseWriter, r *http.Request) {
	p.setCookie(w, r, cookieName, "", "/", -1)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- Dashboard ----

type sectionView struct {
	Key, Title, Kind string
	Enabled          bool
}

type serverView struct {
	MachineID, Name, Owner, Connection, Address, Error, Synced string
	Healthy, Enabled                                           bool
	LatencyMs                                                  int64
	Items                                                      int
	Sections                                                   []sectionView
}

type userView struct {
	ID, Name, Thumb, LastSeen string
	Admin, Me                 bool
}

type langView struct{ Key, Name string }

type page struct {
	Lang          string // interface language
	Nonce         string // Content-Security-Policy nonce of the inline style and script
	Version       string
	FirstRun      bool // login page: nobody has signed in yet
	Account       *accounts.Account
	ManifestURL   string
	StremioURL    string
	WebURL        string
	Langs         []langView // the user's order
	LangOptions   []langView // languages that can still be added
	MaxStreams    int
	Resolutions   map[string]bool
	Mode          string
	Servers       []serverView
	Items         int
	Users         []userView
	TraktReady    bool // the Trakt application is configured
	TraktEnabled  bool
	TraktLinked   bool
	TraktVIP      bool
	TraktUser     string
	TraktSync     bool
	TraktLastSync string
}

// nonce returns a fresh random value for the Content-Security-Policy.
func nonce() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b) // no characters that HTML escapes
}

func (p *Panel) home(w http.ResponseWriter, r *http.Request) {
	a := p.current(r)
	lang := p.lang(r, a)
	pg := page{Lang: lang, Nonce: nonce(), Version: p.Version}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Language", lang)
	h.Set("Vary", "Cookie, Accept-Language")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+pg.Nonce+"'; style-src 'nonce-"+pg.Nonce+"'; "+
		"img-src 'self' data: https:; font-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups") // the Plex sign-in window
	if a == nil {
		pg.FirstRun = len(p.Accounts.All()) == 0
		p.render(w, lang, "login.html", pg)
		return
	}
	base := stremio.BaseURL(p.PublicURL, r)
	manifest := base + "/" + a.ID + "/manifest.json"
	pg.Account, pg.ManifestURL = a, manifest
	pg.StremioURL = "stremio://" + strings.TrimPrefix(strings.TrimPrefix(manifest, "https://"), "http://")
	pg.WebURL = "https://web.stremio.com/#/addons?addon=" + url.QueryEscape(manifest)
	pg.MaxStreams, pg.Mode, pg.Items = a.Prefs.MaxStreams, a.Prefs.StreamMode, p.Store.Count()
	pg.Resolutions = map[string]bool{}
	langs := a.Prefs.Languages
	if len(langs) == 0 {
		langs = p.Languages
	}
	chosen := map[string]bool{}
	for _, l := range langs {
		key := index.NormalizeLanguage(l)
		if key == "" || chosen[key] {
			continue
		}
		chosen[key] = true
		pg.Langs = append(pg.Langs, langView{key, index.LanguageName(key, lang)})
	}
	for _, key := range index.KnownLanguages() {
		if !chosen[key] {
			pg.LangOptions = append(pg.LangOptions, langView{key, index.LanguageName(key, lang)})
		}
	}
	if pg.Mode == "" {
		pg.Mode = accounts.ModeAuto
	}
	for _, res := range a.Prefs.Resolutions {
		pg.Resolutions[res] = true
	}
	if len(pg.Resolutions) == 0 {
		for _, res := range validRes {
			pg.Resolutions[res] = true
		}
	}
	for _, s := range p.Registry.ForAccount(a.ID) {
		sv := serverView{MachineID: s.MachineID, Name: s.Name, Owner: s.Owner, Healthy: s.Healthy(), Error: s.LastError, Enabled: a.ServerAllowed(s.MachineID)}
		if s.Best != nil {
			sv.Connection, sv.Address, sv.LatencyMs = i18n.T(lang, "conn."+s.Best.Kind()), s.Best.Address, s.Best.Latency.Milliseconds()
		}
		if sd := p.Store.Server(s.MachineID); sd != nil {
			sv.Items = len(sd.Items)
			if sd.SyncedAt > 0 {
				sv.Synced = i18n.Ago(lang, sd.SyncedAt)
			}
			for _, sec := range sd.Sections {
				kind := i18n.T(lang, "kind.movie")
				if sec.Type == "show" {
					kind = i18n.T(lang, "kind.show")
				}
				sv.Sections = append(sv.Sections, sectionView{Key: sec.Key, Title: sec.Title, Kind: kind, Enabled: a.SectionAllowed(s.MachineID, sec.Key)})
			}
		}
		pg.Servers = append(pg.Servers, sv)
	}
	if a.Admin {
		for _, u := range p.Accounts.All() {
			pg.Users = append(pg.Users, userView{ID: u.ID, Name: u.Username, Thumb: u.Thumb, Admin: u.Admin, Me: u.ID == a.ID, LastSeen: i18n.Ago(lang, u.LastSeen)})
		}
	}
	t := a.Prefs.Trakt
	pg.TraktReady = p.Trakt != nil && p.Trakt.Client.Configured()
	pg.TraktEnabled, pg.TraktLinked, pg.TraktVIP, pg.TraktUser, pg.TraktSync = t.Enabled, t.Linked(), t.VIP, t.Username, t.SyncWatched
	if t.LastSync > 0 {
		pg.TraktLastSync = i18n.Ago(lang, t.LastSync)
	}
	p.render(w, lang, "home.html", pg)
}

func (p *Panel) render(w http.ResponseWriter, lang, name string, pg page) {
	if err := p.tmpl[i18n.Or(lang, i18n.ES)].ExecuteTemplate(w, name, pg); err != nil {
		p.Log.Warn("panel: template", "template", name, "error", err)
	}
}

// ---- Preferences ----

var validRes = []string{"4k", "1440", "1080", "720", "sd"}

func (p *Panel) savePrefs(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	lang := p.lang(r, a)
	if err := r.ParseForm(); err != nil {
		jsonError(w, http.StatusBadRequest, i18n.T(lang, "err.form"))
		return
	}
	f := r.Form
	var langs []string
	for _, l := range strings.Split(f.Get("languages"), ",") {
		if l = index.NormalizeLanguage(strings.TrimSpace(l)); l != "" && l != index.Unknown && len(langs) < 32 {
			langs = append(langs, l)
		}
	}
	maxStreams, _ := strconv.Atoi(f.Get("max_streams"))
	var res []string
	for _, v := range validRes {
		if f.Get("res_"+v) == "on" {
			res = append(res, v)
		}
	}
	if len(res) == len(validRes) {
		res = nil // all allowed
	}
	mode := f.Get("mode")
	if mode != accounts.ModeProxy && mode != accounts.ModeDirect {
		mode = accounts.ModeAuto
	}
	var exServers, exSections []string
	for _, s := range p.Registry.ForAccount(a.ID) {
		if f.Get("server_"+s.MachineID) != "on" {
			exServers = append(exServers, s.MachineID)
		}
		if sd := p.Store.Server(s.MachineID); sd != nil {
			for _, sec := range sd.Sections {
				if f.Get("section_"+s.MachineID+"_"+sec.Key) != "on" {
					exSections = append(exSections, s.MachineID+"/"+sec.Key)
				}
			}
		}
	}
	sort.Strings(exServers)
	sort.Strings(exSections)
	_, err := p.Accounts.Update(a.ID, func(a *accounts.Account) {
		a.Prefs.Languages = langs
		a.Prefs.MaxStreams = max(0, min(maxStreams, 100))
		a.Prefs.Resolutions = res
		a.Prefs.StreamMode = mode
		a.Prefs.Transcode = f.Get("transcode") == "on"
		a.Prefs.HideCatalogs = f.Get("catalogs") != "on"
		a.Prefs.ExcludedServers = exServers
		a.Prefs.ExcludedSections = exSections
		a.Prefs.Trakt.Enabled = f.Get("trakt_enabled") == "on"
		a.Prefs.Trakt.SyncWatched = f.Get("trakt_sync") == "on"
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, i18n.T(lang, "err.save"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// newURL gives the account a new addon URL (when the old one leaked). The old one stops working.
func (p *Panel) newURL(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	lang := p.lang(r, a)
	cp := *a
	cp.ID = accounts.RandomID(16)
	if err := p.Accounts.Put(&cp); err != nil {
		jsonError(w, http.StatusInternalServerError, i18n.T(lang, "err.change"))
		return
	}
	if err := p.Accounts.Delete(a.ID); err != nil {
		jsonError(w, http.StatusInternalServerError, i18n.T(lang, "err.change"))
		return
	}
	p.setSession(w, r, cp.ID)
	if p.OnAccountsChanged != nil {
		p.OnAccountsChanged()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (p *Panel) reindex(w http.ResponseWriter, _ *http.Request, _ *accounts.Account) {
	if p.Reindex != nil {
		p.Reindex()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (p *Panel) deleteUser(w http.ResponseWriter, r *http.Request, me *accounts.Account) {
	lang := p.lang(r, me)
	id := r.PathValue("id")
	if id == me.ID {
		jsonError(w, http.StatusBadRequest, i18n.T(lang, "err.self"))
		return
	}
	if err := p.Accounts.Delete(id); err != nil {
		jsonError(w, http.StatusInternalServerError, i18n.T(lang, "err.delete"))
		return
	}
	if p.OnAccountsChanged != nil {
		p.OnAccountsChanged()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- Trakt ----

func (p *Panel) traktStart(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	lang := p.lang(r, a)
	if p.Trakt == nil || !p.Trakt.Client.Configured() {
		jsonError(w, http.StatusBadRequest, i18n.T(lang, "err.trakt.off"))
		return
	}
	dc, err := p.Trakt.Client.StartDevice(r.Context())
	if err != nil {
		jsonError(w, http.StatusBadGateway, i18n.T(lang, "err.trakt.down"))
		return
	}
	writeJSON(w, http.StatusOK, dc)
}

func (p *Panel) traktPoll(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	lang := p.lang(r, a)
	if p.Trakt == nil {
		jsonError(w, http.StatusBadRequest, i18n.T(lang, "err.trakt.off"))
		return
	}
	tok, err := p.Trakt.Client.PollDevice(r.Context(), r.FormValue("device_code"))
	if errors.Is(err, trakt.ErrPending) {
		writeJSON(w, http.StatusOK, map[string]any{"pending": true})
		return
	}
	if err != nil {
		jsonError(w, http.StatusBadGateway, i18n.T(lang, "err.trakt.link"))
		return
	}
	if _, err := p.Trakt.Link(r.Context(), a.ID, tok); err != nil {
		if errors.Is(err, trakt.ErrNotVIP) {
			jsonError(w, http.StatusForbidden, i18n.T(lang, "err.trakt.vip"))
			return
		}
		jsonError(w, http.StatusBadGateway, i18n.T(lang, "err.trakt.account"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (p *Panel) traktUnlink(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	_, err := p.Accounts.Update(a.ID, func(a *accounts.Account) { a.Prefs.Trakt = accounts.Trakt{} })
	if err != nil {
		jsonError(w, http.StatusInternalServerError, i18n.T(p.lang(r, a), "err.trakt.unlink"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
