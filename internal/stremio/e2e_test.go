package stremio

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func audio(tag, title string, ch int) plex.Stream {
	return plex.Stream{StreamType: plex.StreamAudio, LanguageTag: tag, Title: title, Codec: "ac3", Channels: ch}
}

func movie(key, imdb string, updated int64, mediaID int64, res string, height int, size int64, streams ...plex.Stream) plex.Metadata {
	return plex.Metadata{
		RatingKey: key, Type: "movie", Title: "Interstellar", Year: 2014, UpdatedAt: updated,
		Guids: []plex.Guid{{ID: "imdb://" + imdb}, {ID: "tmdb://157336"}},
		Media: []plex.Media{{
			ID: mediaID, VideoResolution: res, Height: height, VideoCodec: "hevc", Container: "mkv", Bitrate: int(size / 1000),
			Parts: []plex.Part{{ID: mediaID, Key: "/library/parts/" + key + "/1/file.mkv", File: "/media/Interstellar " + res + ".mkv", Size: size, Streams: streams}},
		}},
	}
}

type fixture struct {
	tv      *plextest.PlexTV
	own     *plextest.Server
	friend  *plextest.Server
	reg     *servers.Registry
	store   *index.Store
	indexer *index.Indexer
	handler http.Handler
	accs    *accounts.Store
	h       *Handler
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ownMovie := movie("10", "tt0816692", 100, 1, "1080", 1080, 8<<30,
		plex.Stream{StreamType: plex.StreamVideo, Codec: "hevc"},
		audio("en", "English", 6))
	friendMovie := movie("20", "tt0816692", 100, 2, "4k", 2160, 58<<30,
		plex.Stream{StreamType: plex.StreamVideo, Codec: "hevc", DOVIPresent: true},
		audio("es", "Castellano", 6), audio("en", "", 8),
		plex.Stream{StreamType: plex.StreamSubtitle, LanguageTag: "es", Forced: true},
		plex.Stream{StreamType: plex.StreamSubtitle, LanguageTag: "es"})
	latinMovie := movie("21", "tt0816692", 100, 3, "720", 720, 2<<30, audio("es-419", "", 2))

	show := plex.Metadata{RatingKey: "300", Type: "show", Title: "Juego de tronos", Guids: []plex.Guid{{ID: "imdb://tt0944947"}, {ID: "tvdb://121361"}}}
	episode := plex.Metadata{
		RatingKey: "301", Type: "episode", Title: "Ganas el juego", GrandparentRatingKey: "300", GrandparentTitle: "Juego de tronos",
		ParentIndex: 1, Index: 7, UpdatedAt: 50,
		Media: []plex.Media{{ID: 9, VideoResolution: "1080", Height: 1080, VideoCodec: "h264", Container: "mp4",
			Parts: []plex.Part{{ID: 9, Key: "/library/parts/9/1/file.mp4", File: "GoT.S01E07.mp4", Size: 1 << 30,
				Streams: []plex.Stream{audio("es-ES", "", 2)}}}}},
	}

	own := (&plextest.Server{
		MachineID: "own-id", Token: "own-token", AlsoAccept: "friend-token",
		Sections: []plextest.Section{{Key: "1", Type: "movie", Items: []plex.Metadata{ownMovie}},
			{Key: "2", Type: "show", Shows: []plex.Metadata{show}, Items: []plex.Metadata{episode}}},
		Details: map[string]plex.Metadata{"10": ownMovie, "301": episode},
	}).Start()
	friend := (&plextest.Server{
		MachineID: "friend-id", Token: "friend-token",
		Sections: []plextest.Section{{Key: "5", Type: "movie", Items: []plex.Metadata{friendMovie, latinMovie}}},
		Details:  map[string]plex.Metadata{"20": friendMovie, "21": latinMovie},
	}).Start()
	t.Cleanup(own.Close)
	t.Cleanup(friend.Close)

	tv := (&plextest.PlexTV{Token: "account", Resources: []plex.Resource{
		{Name: "Casa", ClientIdentifier: "own-id", Provides: "server", Owned: true, AccessToken: "own-token",
			Connections: []plex.Connection{
				{URI: "http://127.0.0.1:1", Local: true}, // dead
				{URI: own.URL(), Local: true},
			}},
		{Name: "Cine", ClientIdentifier: "friend-id", Provides: "server", AccessToken: "friend-token", SourceTitle: "juan",
			Connections: []plex.Connection{
				{URI: own.URL(), Local: true}, // the friend's LAN address answers as our own server
				{URI: friend.URL()},
			}},
		{Name: "Móvil", ClientIdentifier: "phone", Provides: "player"},
	}}).Start()
	t.Cleanup(tv.Close)

	client := plex.NewClient("test-client", "test")
	accs := newAccounts(t, accounts.ModeDirect)
	reg := servers.NewRegistry(client, tv.URL(), credentials(accs), nil, 2*time.Second, log)
	store := index.NewStore()
	ix := &index.Indexer{Client: client, Store: store, Log: log, Timeout: 5 * time.Second, PageSize: 1, BatchSize: 1, Concurrency: 2}
	h, handler := newHandler(store, reg, accs, []string{index.Castellano, index.Latino, "ingles"})
	return &fixture{tv: tv, own: own, friend: friend, reg: reg, store: store, indexer: ix, handler: handler, accs: accs, h: h}
}

func (f *fixture) syncAll(t *testing.T) []index.Stats {
	t.Helper()
	ctx := context.Background()
	var all []index.Stats
	for _, s := range f.reg.All() {
		st, err := f.indexer.Sync(ctx, index.Source{MachineID: s.MachineID, Name: s.Label(), Owner: s.Owner, Owned: s.Owned, API: s.API()})
		if err != nil {
			t.Fatalf("sync %s: %v", s.Name, err)
		}
		all = append(all, st)
	}
	return all
}

func get(t *testing.T, h http.Handler, path string) []Stream {
	t.Helper()
	rec := httptest.NewRecorder()
	if !strings.HasPrefix(path, "/"+testAcc+"/") {
		path = accPath(path)
	}
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d", path, rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("%s: missing CORS header", path)
	}
	var body struct{ Streams []Stream }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Streams
}

func TestEndToEnd(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.reg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(f.reg.All()); n != 2 {
		t.Fatalf("servers = %d, want 2 (players are ignored)", n)
	}
	f.reg.Probe(ctx)
	own, friend := f.reg.Get("own-id"), f.reg.Get("friend-id")
	if own.Best == nil || own.Best.URI != f.own.URL() {
		t.Fatalf("own server should use the working local connection, got %+v", own.Best)
	}
	if friend.Best == nil || friend.Best.URI != f.friend.URL() {
		t.Fatalf("friend server must not use an address that answers as another server, got %+v", friend.Best)
	}

	f.syncAll(t)
	if got := f.store.Count(); got != 4 {
		t.Fatalf("indexed items = %d, want 4", got)
	}

	streams := get(t, f.handler, "/stream/movie/tt0816692.json")
	if len(streams) != 3 {
		t.Fatalf("streams = %d, want 3", len(streams))
	}
	// Castilian first (4K DV from the friend), then Latin American, then English only.
	if !strings.Contains(streams[0].Name, "4K DV") || !strings.Contains(streams[0].Description, "Castellano 5.1") {
		t.Fatalf("first stream should be the Castilian 4K DV one:\n%s\n%s", streams[0].Name, streams[0].Description)
	}
	if !strings.Contains(streams[0].Description, "Castellano (forzados), Castellano") {
		t.Fatalf("subtitles missing: %s", streams[0].Description)
	}
	if !strings.Contains(streams[0].Description, "Cine (juan) · Remota") {
		t.Fatalf("server label missing: %s", streams[0].Description)
	}
	if !strings.Contains(streams[1].Description, "Latino") || !strings.Contains(streams[2].Description, "Inglés 5.1") {
		t.Fatalf("order should be castellano, latino, ingles:\n%s\n---\n%s", streams[1].Description, streams[2].Description)
	}
	if want := f.friend.URL() + "/library/parts/20/1/file.mkv?X-Plex-Token=friend-token"; streams[0].URL != want {
		t.Fatalf("url = %s, want %s", streams[0].URL, want)
	}
	if streams[0].BehaviorHints.VideoSize != 58<<30 || streams[0].BehaviorHints.Filename != "Interstellar 4k.mkv" || !streams[0].BehaviorHints.NotWebReady {
		t.Fatalf("behavior hints: %+v", streams[0].BehaviorHints)
	}

	tmdb := get(t, f.handler, "/stream/movie/tmdb:157336.json")
	if len(tmdb) != 3 {
		t.Fatalf("tmdb lookup = %d streams, want 3", len(tmdb))
	}

	eps := get(t, f.handler, "/stream/series/tt0944947:1:7.json")
	if len(eps) != 1 || !strings.Contains(eps[0].Description, "T1E07") || eps[0].BehaviorHints.NotWebReady {
		t.Fatalf("episode streams: %+v", eps)
	}
	if got := get(t, f.handler, "/stream/series/tt0944947%3A1%3A7.json"); len(got) != 1 {
		t.Fatalf("url-encoded series id: %d streams", len(got))
	}
	if got := get(t, f.handler, "/stream/series/tt0944947:1:8.json"); len(got) != 0 {
		t.Fatalf("missing episode should give no streams, got %d", len(got))
	}
}

func TestIncrementalSyncReusesUnchangedItems(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.reg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	f.reg.Probe(ctx)
	f.syncAll(t)
	callsAfterFirst := f.own.MetadataCalls.Load() + f.friend.MetadataCalls.Load()

	stats := f.syncAll(t)
	for _, st := range stats {
		if st.Detailed != 0 || st.Reused != st.Items {
			t.Fatalf("second sync should reuse everything: %+v", st)
		}
	}
	if got := f.own.MetadataCalls.Load() + f.friend.MetadataCalls.Load(); got != callsAfterFirst {
		t.Fatalf("unchanged items were fetched again (%d -> %d calls)", callsAfterFirst, got)
	}

	// A changed item (new updatedAt) is read again.
	changed := movie("10", "tt0816692", 200, 1, "1080", 1080, 8<<30, audio("es", "Castellano", 2))
	f.own.Sections[0].Items = []plex.Metadata{changed}
	f.own.SetDetail(changed)
	stats = f.syncAll(t)
	var detailed int
	for _, st := range stats {
		detailed += st.Detailed
	}
	if detailed != 1 {
		t.Fatalf("detailed = %d, want 1", detailed)
	}
	streams := get(t, f.handler, "/stream/movie/tt0816692.json")
	var found bool
	for _, s := range streams {
		if strings.Contains(s.Description, "Casa") && strings.Contains(s.Description, "Castellano 2.0") {
			found = true
		}
	}
	if !found {
		t.Fatal("changed item not updated in the index")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.reg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	f.reg.Probe(ctx)
	f.syncAll(t)
	path := filepath.Join(t.TempDir(), "index.gob")
	if err := f.store.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded := index.NewStore()
	if err := loaded.Load(path); err != nil {
		t.Fatal(err)
	}
	if loaded.Count() != f.store.Count() || len(loaded.Lookup("imdb:tt0816692")) != 3 || len(loaded.Lookup("imdb:tt0944947:1:7")) != 1 {
		t.Fatalf("snapshot lost data: %d items", loaded.Count())
	}
}

func TestUnreachableServerIsSkipped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.reg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	f.reg.Probe(ctx)
	f.syncAll(t)
	f.friend.Close()
	f.reg.Probe(ctx)
	if f.reg.Get("friend-id").Healthy() {
		t.Fatal("friend server should be marked unreachable")
	}
	streams := get(t, f.handler, "/stream/movie/tt0816692.json")
	if len(streams) != 1 || !strings.Contains(streams[0].Description, "Casa") {
		t.Fatalf("only the own server should answer, got %d streams", len(streams))
	}
}

func TestPlexTVMustBeAskedForJSON(t *testing.T) {
	f := newFixture(t)
	// The client always sends Accept: application/json; the fake answers XML otherwise.
	if err := f.reg.Refresh(context.Background()); err != nil {
		t.Fatalf("resources should decode as JSON: %v", err)
	}
}

func TestManifestAndAccountPrefix(t *testing.T) {
	f := newFixture(t)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, accPath("/manifest.json"), nil))
	var m manifest
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &m) != nil || m.Version != "1.2.3" || m.ID != "com.jmoruno.plexmor" {
		t.Fatalf("manifest: %d %s", rec.Code, rec.Body)
	}
	for _, p := range []string{"/manifest.json", "/otra/manifest.json", "/otra/stream/movie/tt0816692.json"} {
		rec = httptest.NewRecorder()
		f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s without a valid account should be hidden, got %d", p, rec.Code)
		}
	}
}

func TestEnglishTexts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.reg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	f.reg.Probe(ctx)
	f.syncAll(t)
	if _, err := f.accs.Update(testAcc, func(a *accounts.Account) { a.Prefs.UILanguage = "en" }); err != nil {
		t.Fatal(err)
	}
	streams := get(t, f.handler, "/stream/movie/tt0816692.json")
	if len(streams) != 3 || !strings.Contains(streams[0].Description, "Spanish (Spain) (forced), Spanish (Spain)") ||
		!strings.Contains(streams[0].Description, "Cine (juan) · Remote") || !strings.Contains(streams[2].Description, "English 5.1") {
		t.Fatalf("English stream texts:\n%s", streams[0].Description)
	}
	eps := get(t, f.handler, "/stream/series/tt0944947:1:7.json")
	if len(eps) != 1 || !strings.Contains(eps[0].Description, "S1E07") {
		t.Fatalf("English episode label: %+v", eps)
	}
	m := decode[manifest](t, fetch(t, f.handler, accPath("/manifest.json"), nil))
	if !strings.Contains(m.Description, "Account: ana") || m.Catalogs[0].Name != "Plexmor · Recently added" {
		t.Fatalf("English manifest: %s / %s", m.Description, m.Catalogs[0].Name)
	}
	// The server default applies to accounts that have not chosen.
	_, _ = f.accs.Update(testAcc, func(a *accounts.Account) { a.Prefs.UILanguage = "" })
	f.h.Language = "en"
	if m := decode[manifest](t, fetch(t, f.handler, accPath("/manifest.json"), nil)); m.Catalogs[0].Name != "Plexmor · Recently added" {
		t.Fatalf("server default: %s", m.Catalogs[0].Name)
	}
}

func TestCORSLeavesThePanelOut(t *testing.T) {
	h := CORS(http.NotFoundHandler())
	for path, want := range map[string]string{"/acc/manifest.json": "*", "/api/prefs": "", "/": "", "/login/check": "", "/metrics": ""} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("%s: Access-Control-Allow-Origin = %q, want %q", path, got, want)
		}
	}
}
