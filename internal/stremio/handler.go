package stremio

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/i18n"
	"github.com/jmoruno/plexmor/internal/index"
	"github.com/jmoruno/plexmor/internal/metrics"
	"github.com/jmoruno/plexmor/internal/plex"
	"github.com/jmoruno/plexmor/internal/servers"
)

// TraktLists gives the Trakt catalogs of an account (optional module).
type TraktLists interface {
	// Available reports whether Trakt is configured, enabled and linked with a VIP account.
	Available(a *accounts.Account) bool
	// List returns the IMDb ids of a Trakt list ("watchlist" or "recommended") for movie or series.
	List(ctx context.Context, a *accounts.Account, kind, list string) ([]string, error)
}

// Handler serves the Stremio addon of every account under /{account}/.
type Handler struct {
	Store    *index.Store
	Registry *servers.Registry
	Accounts *accounts.Store
	Client   *plex.Client
	Discover string // https://discover.provider.plex.tv
	// PublicURL is how clients reach Plexmor; when empty it is taken from each request.
	PublicURL string
	Version   string
	Log       *slog.Logger
	Metrics   *metrics.Registry
	Trakt     TraktLists // nil when the module is off
	// Defaults for accounts that have not chosen.
	Languages  []string
	MaxStreams int
	Language   string // texts: i18n.ES or i18n.EN

	proxyClient *http.Client
	live        liveCache
}

// Routes registers the addon routes on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	h.proxyClient = newProxyClient()
	m := h.Metrics
	mux.HandleFunc("GET /{acc}/manifest.json", m.Wrap("manifest", h.withAccount(h.manifest)))
	mux.HandleFunc("GET /{acc}/configure", h.withAccount(h.configure))
	mux.HandleFunc("GET /{acc}/stream/{type}/{id}", m.Wrap("stream", h.withAccount(h.stream)))
	mux.HandleFunc("GET /{acc}/catalog/{type}/{id}", m.Wrap("catalog", h.withAccount(h.catalog)))
	mux.HandleFunc("GET /{acc}/catalog/{type}/{id}/{extra}", m.Wrap("catalog", h.withAccount(h.catalog)))
	mux.HandleFunc("GET /{acc}/meta/{type}/{id}", m.Wrap("meta", h.withAccount(h.meta)))
	mux.HandleFunc("GET /{acc}/subtitles/{type}/{id}", m.Wrap("subtitles", h.withAccount(h.subtitles)))
	mux.HandleFunc("GET /{acc}/subtitles/{type}/{id}/{extra}", m.Wrap("subtitles", h.withAccount(h.subtitles)))
	mux.HandleFunc("GET /{acc}/play/{server}/{path...}", m.Wrap("play", h.withAccount(h.play)))
	mux.HandleFunc("HEAD /{acc}/play/{server}/{path...}", h.withAccount(h.play))
	mux.HandleFunc("GET /{acc}/img/{server}/{path...}", m.Wrap("image", h.withAccount(h.image)))
	mux.HandleFunc("GET /{acc}/health", h.withAccount(h.health))
}

// withAccount resolves the {acc} path segment; unknown accounts get a 404 so URLs cannot be guessed.
func (h *Handler) withAccount(next func(http.ResponseWriter, *http.Request, *accounts.Account)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := h.Accounts.Get(r.PathValue("acc"))
		if a == nil {
			http.NotFound(w, r)
			return
		}
		h.Accounts.Touch(a.ID)
		next(w, r, a)
	}
}

// BaseURL is the public address of Plexmor as seen by the client of r.
func (h *Handler) BaseURL(r *http.Request) string {
	return BaseURL(h.PublicURL, r)
}

// BaseURL returns public when set, otherwise the scheme and host the request came through
// (honoring X-Forwarded-Proto and X-Forwarded-Host from a reverse proxy).
func BaseURL(public string, r *http.Request) string {
	if public != "" {
		return strings.TrimRight(public, "/")
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
		if r.TLS != nil {
			proto = "https"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return proto + "://" + host
}

// lang is the language of the texts an account sees in Stremio.
func (h *Handler) lang(a *accounts.Account) string {
	return i18n.Or(a.Prefs.UILanguage, i18n.Or(h.Language, i18n.ES))
}

// options turns the preferences of an account into stream options.
func (h *Handler) options(r *http.Request, a *accounts.Account) StreamOptions {
	p := a.Prefs
	langs := p.Languages
	if len(langs) == 0 {
		langs = h.Languages
	}
	normalized := make([]string, 0, len(langs))
	for _, l := range langs {
		normalized = append(normalized, index.NormalizeLanguage(l))
	}
	max := p.MaxStreams
	if max == 0 {
		max = h.MaxStreams
	}
	mode := p.StreamMode
	if mode == "" {
		mode = accounts.ModeAuto
	}
	return StreamOptions{
		Lang:      h.lang(a),
		AccountID: a.ID, Languages: normalized, MaxStreams: max, Resolutions: p.Resolutions,
		Mode: mode, BaseURL: h.BaseURL(r), Transcode: p.Transcode, Allowed: a.SectionAllowed,
	}
}

type manifest struct {
	ID            string         `json:"id"`
	Version       string         `json:"version"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Logo          string         `json:"logo,omitempty"`
	Resources     []any          `json:"resources"`
	Types         []string       `json:"types"`
	IDPrefixes    []string       `json:"idPrefixes"`
	Catalogs      []catalogDef   `json:"catalogs"`
	BehaviorHints map[string]any `json:"behaviorHints"`
}

type resource struct {
	Name       string   `json:"name"`
	Types      []string `json:"types"`
	IDPrefixes []string `json:"idPrefixes"`
}

func (h *Handler) manifest(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	types := []string{"movie", "series"}
	res := []any{
		resource{Name: "stream", Types: types, IDPrefixes: []string{"tt", "tmdb:", "tvdb:", "plexmor:"}},
		resource{Name: "subtitles", Types: types, IDPrefixes: []string{"tt", "plexmor:"}},
	}
	var catalogs []catalogDef
	if !a.Prefs.HideCatalogs {
		catalogs = h.catalogDefs(a)
		res = append(res, "catalog", resource{Name: "meta", Types: types, IDPrefixes: []string{"plexmor:"}})
	}
	if catalogs == nil {
		catalogs = []catalogDef{}
	}
	lang := h.lang(a)
	desc := i18n.T(lang, "addon.desc")
	if a.Username != "" {
		desc += i18n.T(lang, "addon.account", a.Username)
	}
	writeJSON(w, http.StatusOK, "private, max-age=300", manifest{
		ID:            "com.jmoruno.plexmor",
		Version:       manifestVersion(h.Version),
		Name:          "Plexmor",
		Description:   desc,
		Logo:          h.BaseURL(r) + "/static/logo.png",
		Resources:     res,
		Types:         types,
		IDPrefixes:    []string{"tt", "tmdb:", "tvdb:", "plexmor:"},
		Catalogs:      catalogs,
		BehaviorHints: map[string]any{"configurable": true},
	})
}

// configure is where Stremio's "Configure" button lands: the panel.
func (h *Handler) configure(w http.ResponseWriter, r *http.Request, _ *accounts.Account) {
	http.Redirect(w, r, h.BaseURL(r)+"/", http.StatusFound)
}

// manifestVersion turns build versions such as "v0.1.0" or "dev" into the semver Stremio expects.
func manifestVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	if strings.Count(v, ".") >= 2 {
		return v
	}
	return "0.0.0"
}

// items finds the indexed items for a Stremio id.
func (h *Handler) items(kind, id string) []*index.Item {
	if server, rk, ok := index.ParsePlexmorID(id); ok {
		if it := h.Store.Item(server, rk); it != nil {
			return []*index.Item{it}
		}
		return nil
	}
	var items []*index.Item
	for _, key := range lookupKeys(kind, id) {
		items = append(items, h.Store.Lookup(key)...)
	}
	return items
}

func (h *Handler) stream(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	start := time.Now()
	kind := r.PathValue("type")
	id := strings.TrimSuffix(r.PathValue("id"), ".json")
	streams := BuildStreams(h.items(kind, id), h.Registry.Get, h.options(r, a))
	h.Log.Debug("streams", "type", kind, "id", id, "streams", len(streams), "elapsed", time.Since(start))
	// Direct stream URLs carry Plex tokens: never let shared caches keep them.
	cache := "private, max-age=60"
	if len(streams) == 0 {
		cache = "private, max-age=30"
	}
	writeJSON(w, http.StatusOK, cache, map[string]any{"streams": streams})
}

type serverHealth struct {
	Name       string `json:"name"`
	Owner      string `json:"owner,omitempty"`
	Healthy    bool   `json:"healthy"`
	Connection string `json:"connection,omitempty"`
	Address    string `json:"address,omitempty"`
	LatencyMs  int64  `json:"latencyMs,omitempty"`
	Items      int    `json:"items"`
	SyncedAt   string `json:"syncedAt,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ServerHealth describes the servers an account can use.
func ServerHealth(store *index.Store, list []*servers.Server) []serverHealth {
	out := []serverHealth{}
	for _, s := range list {
		sh := serverHealth{Name: s.Name, Owner: s.Owner, Healthy: s.Healthy(), Error: s.LastError}
		if s.Best != nil {
			sh.Connection, sh.Address, sh.LatencyMs = s.Best.Kind(), s.Best.Address, s.Best.Latency.Milliseconds()
		}
		if sd := store.Server(s.MachineID); sd != nil {
			sh.Items = len(sd.Items)
			if sd.SyncedAt > 0 {
				sh.SyncedAt = time.Unix(sd.SyncedAt, 0).UTC().Format(time.RFC3339)
			}
		}
		out = append(out, sh)
	}
	return out
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request, a *accounts.Account) {
	writeJSON(w, http.StatusOK, "no-store", map[string]any{
		"version": h.Version,
		"items":   h.Store.Count(),
		"servers": ServerHealth(h.Store, h.Registry.ForAccount(a.ID)),
	})
}

func writeJSON(w http.ResponseWriter, status int, cache string, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", cache)
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// CORS lets Stremio Web and other browser clients call the addon. The panel and its API
// are left out: other sites have no business reading them.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if panelPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// panelPath reports whether p belongs to the web panel rather than to the addon.
func panelPath(p string) bool {
	switch p {
	case "/", "/lang", "/logout", "/metrics":
		return true
	}
	return strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/login/")
}
