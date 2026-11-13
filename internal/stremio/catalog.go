package stremio

import (
	"context"
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
)

const pageSize = 100

type extraDef struct {
	Name       string   `json:"name"`
	IsRequired bool     `json:"isRequired,omitempty"`
	Options    []string `json:"options,omitempty"`
}

type catalogDef struct {
	Type  string     `json:"type"`
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Extra []extraDef `json:"extra,omitempty"`
}

// MetaPreview is an item of a catalog.
type MetaPreview struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Poster      string   `json:"poster,omitempty"`
	PosterShape string   `json:"posterShape,omitempty"`
	Background  string   `json:"background,omitempty"`
	ReleaseInfo string   `json:"releaseInfo,omitempty"`
	Description string   `json:"description,omitempty"`
	Genres      []string `json:"genres,omitempty"`
}

// catalogDefs lists the catalogs of an account: shared ones, then one per library.
func (h *Handler) catalogDefs(a *accounts.Account) []catalogDef {
	skip := extraDef{Name: "skip"}
	var defs []catalogDef
	lang := h.lang(a)
	for _, kind := range []string{"movie", "series"} {
		defs = append(defs,
			catalogDef{Type: kind, ID: "recent", Name: i18n.T(lang, "cat.recent"), Extra: []extraDef{skip}},
			catalogDef{Type: kind, ID: "ondeck", Name: i18n.T(lang, "cat.ondeck")},
			catalogDef{Type: kind, ID: "watchlist", Name: i18n.T(lang, "cat.watchlist"), Extra: []extraDef{skip}},
		)
		if h.Trakt != nil && h.Trakt.Available(a) {
			defs = append(defs,
				catalogDef{Type: kind, ID: "trakt.watchlist", Name: i18n.T(lang, "cat.trakt.watch"), Extra: []extraDef{skip}},
				catalogDef{Type: kind, ID: "trakt.recommended", Name: i18n.T(lang, "cat.trakt.rec"), Extra: []extraDef{skip}},
			)
		}
		defs = append(defs, catalogDef{Type: kind, ID: "search", Name: "Plexmor", Extra: []extraDef{{Name: "search", IsRequired: true}, skip}})
	}
	for _, s := range h.Registry.ForAccount(a.ID) {
		sd := h.Store.Server(s.MachineID)
		if sd == nil || !a.ServerAllowed(s.MachineID) {
			continue
		}
		for _, sec := range sd.Sections {
			if !a.SectionAllowed(s.MachineID, sec.Key) {
				continue
			}
			kind := "movie"
			if sec.Type == "show" {
				kind = "series"
			}
			defs = append(defs, catalogDef{
				Type: kind, ID: "lib." + s.MachineID + "." + sec.Key, Name: sec.Title + " · " + s.Label(),
				Extra: []extraDef{skip, {Name: "genre", Options: h.genres(s.MachineID, sec.Key)}},
			})
		}
	}
	return defs
}

// genres lists the most common genres of a library, for the catalog filter.
func (h *Handler) genres(server, section string) []string {
	count := map[string]int{}
	for _, e := range h.Store.Entries() {
		if e.Server == server && e.Section == section {
			for _, g := range e.Genres {
				count[g]++
			}
		}
	}
	out := make([]string, 0, len(count))
	for g := range count {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if count[out[i]] != count[out[j]] {
			return count[out[i]] > count[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > 30 {
		out = out[:30]
	}
	sort.Strings(out)
	return out
}

// parseExtra reads "skip=100&genre=Acción" (Stremio's extra path segment).
func parseExtra(r *http.Request) url.Values {
	raw := strings.TrimSuffix(r.PathValue("extra"), ".json")
	v, _ := url.ParseQuery(raw)
	if v == nil {
		v = url.Values{}
	}
	return v
}

func (h *Handler) catalog(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	kind := r.PathValue("type")
	id := strings.TrimSuffix(r.PathValue("id"), ".json")
	extra := parseExtra(r)
	skip, _ := strconv.Atoi(extra.Get("skip"))
	base := h.BaseURL(r)
	ctx := r.Context()

	var metas []MetaPreview
	cache := "private, max-age=300"
	switch {
	case id == "recent":
		metas = h.previews(base, a, h.entries(a, kind, func(*index.Entry) bool { return true }), skip)
	case id == "search":
		q := index.Fold(extra.Get("search"))
		found := h.entries(a, kind, func(e *index.Entry) bool { return e.Matches(q) })
		sort.SliceStable(found, func(i, j int) bool { return found[i].SearchRank(q) < found[j].SearchRank(q) })
		metas = h.previews(base, a, found, skip)
	case strings.HasPrefix(id, "lib."):
		server, section, _ := strings.Cut(strings.TrimPrefix(id, "lib."), ".")
		genre := extra.Get("genre")
		metas = h.previews(base, a, h.entries(a, kind, func(e *index.Entry) bool {
			return e.Server == server && e.Section == section && (genre == "" || contains(e.Genres, genre))
		}), skip)
	case id == "ondeck":
		metas = h.onDeck(ctx, base, a, kind)
		cache = "private, max-age=60"
	case id == "watchlist":
		metas = h.watchlist(ctx, a, kind, skip)
	case strings.HasPrefix(id, "trakt.") && h.Trakt != nil && h.Trakt.Available(a):
		metas = h.traktList(ctx, base, a, kind, strings.TrimPrefix(id, "trakt."), skip)
	}
	if metas == nil {
		metas = []MetaPreview{}
	}
	writeJSON(w, http.StatusOK, cache, map[string]any{"metas": metas})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// entries returns the entries of a kind that the account can see and that match keep.
func (h *Handler) entries(a *accounts.Account, kind string, keep func(*index.Entry) bool) []*index.Entry {
	usable := map[string]bool{}
	for _, s := range h.Registry.ForAccount(a.ID) {
		usable[s.MachineID] = true
	}
	var out []*index.Entry
	for _, e := range h.Store.Entries() {
		if e.Kind == kind && usable[e.Server] && a.SectionAllowed(e.Server, e.Section) && keep(e) {
			out = append(out, e)
		}
	}
	return out
}

// previews turns entries into one page of catalog items, merging the same title on several servers.
func (h *Handler) previews(base string, a *accounts.Account, entries []*index.Entry, skip int) []MetaPreview {
	seen := map[string]bool{}
	var out []MetaPreview
	n := 0
	for _, e := range entries {
		id := e.ID()
		if seen[id] {
			continue
		}
		seen[id] = true
		if n++; n <= skip {
			continue
		}
		out = append(out, h.preview(base, a, e))
		if len(out) == pageSize {
			break
		}
	}
	return out
}

func (h *Handler) preview(base string, a *accounts.Account, e *index.Entry) MetaPreview {
	m := MetaPreview{
		ID: e.ID(), Type: e.Kind, Name: e.Title, PosterShape: "poster",
		Poster: h.imageURL(base, a, e.Server, e.Thumb, 300, 450),
		Genres: e.Genres,
	}
	if e.Year > 0 {
		m.ReleaseInfo = strconv.Itoa(e.Year)
	}
	return m
}

// imageURL is a Plex image served through Plexmor (Plex needs the token, the client never sees it).
func (h *Handler) imageURL(base string, a *accounts.Account, server, plexPath string, w, hgt int) string {
	if plexPath == "" {
		return ""
	}
	if strings.HasPrefix(plexPath, "http://") || strings.HasPrefix(plexPath, "https://") {
		return plexPath
	}
	return base + "/" + a.ID + "/img/" + server + plexPath + "?w=" + strconv.Itoa(w) + "&h=" + strconv.Itoa(hgt)
}

// liveCache keeps per-account answers that come from Plex (on deck, watchlist) for a short time.
type liveCache struct {
	mu   sync.Mutex
	data map[string]liveEntry
}

type liveEntry struct {
	at    time.Time
	value any
}

func (c *liveCache) get(key string, ttl time.Duration, load func() any) any {
	c.mu.Lock()
	if c.data == nil {
		c.data = map[string]liveEntry{}
	}
	if e, ok := c.data[key]; ok && time.Since(e.at) < ttl {
		c.mu.Unlock()
		return e.value
	}
	c.mu.Unlock()
	v := load()
	c.mu.Lock()
	c.data[key] = liveEntry{time.Now(), v}
	c.mu.Unlock()
	return v
}

// onDeck asks every usable server what the account is watching.
func (h *Handler) onDeck(ctx context.Context, base string, a *accounts.Account, kind string) []MetaPreview {
	all := h.live.get("ondeck/"+a.ID, time.Minute, func() any {
		type found struct {
			server string
			m      plex.Metadata
		}
		var mu sync.Mutex
		var list []found
		var wg sync.WaitGroup
		for _, s := range h.Registry.ForAccount(a.ID) {
			if !s.Healthy() || !a.ServerAllowed(s.MachineID) {
				continue
			}
			wg.Add(1)
			go func(machine string, api plex.Server) {
				defer wg.Done()
				c, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				items, err := h.Client.OnDeck(c, api)
				if err != nil {
					h.Log.Debug("on deck failed", "server", machine, "error", err)
					return
				}
				mu.Lock()
				for _, m := range items {
					list = append(list, found{machine, m})
				}
				mu.Unlock()
			}(s.MachineID, plex.Server{URI: s.Best.URI, Token: s.TokenFor(a.ID)})
		}
		wg.Wait()
		var entries []*index.Entry
		for _, f := range list {
			if e := h.entryFor(f.server, f.m); e != nil {
				entries = append(entries, e)
			}
		}
		return entries
	}).([]*index.Entry)
	var keep []*index.Entry
	for _, e := range all {
		if e.Kind == kind {
			keep = append(keep, e)
		}
	}
	return h.previews(base, a, keep, 0)
}

// entryFor maps an item returned by Plex to its catalog entry (an episode maps to its series).
func (h *Handler) entryFor(server string, m plex.Metadata) *index.Entry {
	key, kind := m.RatingKey, "movie"
	if m.Type == "episode" {
		key, kind = m.GrandparentRatingKey, "series"
	}
	for _, e := range h.Store.Entries() {
		if e.Server == server && e.RatingKey == key && e.Kind == kind {
			return e
		}
	}
	return nil
}

// watchlist reads the Plex watchlist of the account (titles with an IMDb id, available or not).
func (h *Handler) watchlist(ctx context.Context, a *accounts.Account, kind string, skip int) []MetaPreview {
	items := h.live.get("watchlist/"+a.ID, 5*time.Minute, func() any {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		list, err := h.Client.Watchlist(c, h.Discover, a.PlexToken)
		if err != nil {
			h.Log.Debug("watchlist failed", "account", a.ID, "error", err)
		}
		return list
	}).([]plex.Metadata)
	var out []MetaPreview
	n := 0
	for _, m := range items {
		t := "movie"
		if m.Type == "show" {
			t = "series"
		}
		ids := index.ParseIDs(m.Guids, "")
		if t != kind || ids.IMDb == "" {
			continue
		}
		if n++; n <= skip {
			continue
		}
		p := MetaPreview{ID: ids.IMDb, Type: t, Name: m.Title, Poster: m.Thumb, PosterShape: "poster"}
		if m.Year > 0 {
			p.ReleaseInfo = strconv.Itoa(m.Year)
		}
		out = append(out, p)
		if len(out) == pageSize {
			break
		}
	}
	return out
}

// traktList shows a Trakt list limited to what the account's servers have.
func (h *Handler) traktList(ctx context.Context, base string, a *accounts.Account, kind, list string, skip int) []MetaPreview {
	ids, err := h.Trakt.List(ctx, a, kind, list)
	if err != nil {
		h.Log.Debug("trakt list failed", "account", a.ID, "list", list, "error", err)
		return nil
	}
	byIMDb := map[string]*index.Entry{}
	for _, e := range h.entries(a, kind, func(e *index.Entry) bool { return e.IDs.IMDb != "" }) {
		if _, ok := byIMDb[e.IDs.IMDb]; !ok {
			byIMDb[e.IDs.IMDb] = e
		}
	}
	var entries []*index.Entry
	for _, id := range ids {
		if e := byIMDb[id]; e != nil {
			entries = append(entries, e)
		}
	}
	return h.previews(base, a, entries, skip)
}
