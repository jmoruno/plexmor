package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jmoruno/plexmor/internal/index"
	"github.com/jmoruno/plexmor/internal/plex"
	"github.com/jmoruno/plexmor/internal/servers"
)

func readyFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.indexer.RetryDelay = time.Millisecond
	f.indexer.MaxConsecutiveFailures = 3
	ctx := context.Background()
	if err := f.reg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	f.reg.Probe(ctx)
	f.syncAll(t)
	return f
}

func syncOne(f *fixture, machineID string) (index.Stats, error) {
	s := f.reg.Get(machineID)
	return f.indexer.Sync(context.Background(), index.Source{MachineID: s.MachineID, Name: s.Label(), Owner: s.Owner, Owned: s.Owned, API: s.API()})
}

func TestFailedLibraryKeepsItsPreviousItems(t *testing.T) {
	f := readyFixture(t)
	f.own.FailSection.Store("2") // series library broken
	st, err := syncOne(f, "own-id")
	if err != nil {
		t.Fatalf("one broken library must not fail the whole server: %v", err)
	}
	if st.FailedSections != 1 {
		t.Fatalf("failed sections = %d, want 1", st.FailedSections)
	}
	if got := get(t, f.handler, "/stream/series/tt0944947:1:7.json"); len(got) != 1 {
		t.Fatalf("episodes of the broken library should be kept, got %d streams", len(got))
	}
	if got := get(t, f.handler, "/stream/movie/tt0816692.json"); len(got) != 3 {
		t.Fatalf("movies should still be served, got %d", len(got))
	}
}

func TestServerDyingDuringDetailsKeepsPreviousIndexQuickly(t *testing.T) {
	f := readyFixture(t)
	before := f.store.Server("friend-id")
	// Every item changed (new updatedAt) but the server stops answering metadata requests.
	for i := range f.friend.Sections[0].Items {
		f.friend.Sections[0].Items[i].UpdatedAt = 999
	}
	f.friend.FailMetadata.Store(true)
	f.indexer.BatchSize = 1 // two batches, both failing
	f.indexer.MaxConsecutiveFailures = 2
	start := time.Now()
	_, err := syncOne(f, "friend-id")
	if !errors.Is(err, index.ErrServerUnresponsive) {
		t.Fatalf("err = %v, want ErrServerUnresponsive", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("aborting took %s", elapsed)
	}
	if f.store.Server("friend-id") != before {
		t.Fatal("the previous index of the server must be kept untouched")
	}
	streams := get(t, f.handler, "/stream/movie/tt0816692.json")
	if !strings.Contains(streams[0].Description, "Castellano") {
		t.Fatal("languages must survive the failed sync")
	}
}

func TestIsolatedBatchFailureKeepsKnownTracks(t *testing.T) {
	f := readyFixture(t)
	f.indexer.MaxConsecutiveFailures = 100 // tolerate failures: the sync completes
	for i := range f.friend.Sections[0].Items {
		f.friend.Sections[0].Items[i].UpdatedAt = 999
	}
	f.friend.FailMetadata.Store(true)
	st, err := syncOne(f, "friend-id")
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending != 2 {
		t.Fatalf("pending = %d, want 2", st.Pending)
	}
	streams := get(t, f.handler, "/stream/movie/tt0816692.json")
	if !strings.Contains(streams[0].Description, "Castellano") {
		t.Fatalf("previous tracks should be kept when details cannot be read:\n%s", streams[0].Description)
	}
	// Next sync with the server healthy reads them again.
	f.friend.FailMetadata.Store(false)
	if st, err = syncOne(f, "friend-id"); err != nil || st.Detailed != 2 {
		t.Fatalf("retry: %+v %v", st, err)
	}
}

func TestRelayRanksAfterDirect(t *testing.T) {
	v4k := index.Version{PartKey: "/a", Resolution: "4k", Detailed: true, Audio: []index.Audio{{Language: index.Castellano}}}
	v1080 := index.Version{PartKey: "/b", Resolution: "1080", Detailed: true, Audio: []index.Audio{{Language: index.Castellano}}}
	relay := &servers.Server{MachineID: "r", Name: "Lejos", Owned: true, Tokens: map[string]string{testAcc: "t"}, Best: &servers.Endpoint{URI: "https://relay", Relay: true}}
	direct := &servers.Server{MachineID: "d", Name: "Cerca", Owned: true, Tokens: map[string]string{testAcc: "t"}, Best: &servers.Endpoint{URI: "http://lan", Local: true}}
	items := []*index.Item{
		{Server: "r", Type: "movie", Versions: []index.Version{v4k}},
		{Server: "d", Type: "movie", Versions: []index.Version{v1080}},
	}
	lookup := func(id string) *servers.Server { return map[string]*servers.Server{"r": relay, "d": direct}[id] }
	got := BuildStreams(items, lookup, StreamOptions{AccountID: testAcc, Languages: []string{index.Castellano}})
	if !strings.Contains(got[0].Description, "Cerca") {
		t.Fatalf("a direct 1080p should beat a 4K over relay:\n%s", got[0].Description)
	}
}

func TestStableOrderOnTies(t *testing.T) {
	srv := &servers.Server{MachineID: "s", Name: "S", Owned: true, Tokens: map[string]string{testAcc: "t"}, Best: &servers.Endpoint{URI: "http://s"}}
	var items []*index.Item
	for _, k := range []string{"/c", "/a", "/b"} {
		items = append(items, &index.Item{Server: "s", Type: "movie", Versions: []index.Version{{PartKey: k, Resolution: "1080"}}})
	}
	first := BuildStreams(items, func(string) *servers.Server { return srv }, StreamOptions{AccountID: testAcc})
	items[0], items[2] = items[2], items[0]
	second := BuildStreams(items, func(string) *servers.Server { return srv }, StreamOptions{AccountID: testAcc})
	for i := range first {
		if first[i].URL != second[i].URL {
			t.Fatal("ties must sort the same way regardless of input order")
		}
	}
}

func TestResolutionRanks(t *testing.T) {
	if resolutionRank(index.Version{Resolution: "1440"}) <= resolutionRank(index.Version{Resolution: "1080"}) {
		t.Fatal("1440p must rank above 1080p")
	}
	if resolutionLabel(index.Version{Height: 1440}) != "1440p" {
		t.Fatal("1440p label")
	}
}

func TestHealthIsPerAccount(t *testing.T) {
	f := readyFixture(t)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nadie/health", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown accounts must not see the servers: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, accPath("/health"), nil))
	if !strings.Contains(rec.Body.String(), "juan") {
		t.Fatalf("the account health should list its servers: %s", rec.Body)
	}
	rec = httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, accPath("/stream/movie/tt0816692.json"), nil))
	if cc := rec.Header().Get("Cache-Control"); !strings.HasPrefix(cc, "private") {
		t.Fatalf("stream responses carry tokens and must be private, got %q", cc)
	}
}

func TestEmptyHealthListsNoServersAsArray(t *testing.T) {
	accs := newAccounts(t, "")
	reg := servers.NewRegistry(plex.NewClient("x", "x"), "http://unused", credentials(accs), nil, time.Second, discard())
	_, h := newHandler(index.NewStore(), reg, accs, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, accPath("/health"), nil))
	var body map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["servers"] == nil {
		t.Fatalf("servers should be [] not null: %s", rec.Body)
	}
}

func TestStreamJSONHasNoTitleAlias(t *testing.T) {
	f := readyFixture(t)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, accPath("/stream/movie/tt0816692.json"), nil))
	var body struct {
		Streams []map[string]any `json:"streams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Streams) == 0 {
		t.Fatalf("bad response: %v %s", err, rec.Body)
	}
	for _, s := range body.Streams {
		if _, dup := s["title"]; dup {
			t.Fatal(`stremio-core treats "title" as an alias of "description": sending both makes Stremio drop every stream`)
		}
	}
}
