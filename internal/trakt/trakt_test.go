package trakt

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/index"
	"github.com/jmoruno/plexmor/internal/plex"
	"github.com/jmoruno/plexmor/internal/plextest"
	"github.com/jmoruno/plexmor/internal/servers"
)

type fakeTrakt struct {
	vip     bool
	polls   atomic.Int32
	refresh atomic.Int32
}

func (f *fakeTrakt) serve(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("trakt-api-key") != "cid" || r.Header.Get("trakt-api-version") != "2" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		authed := r.Header.Get("Authorization") == "Bearer access-1" || r.Header.Get("Authorization") == "Bearer access-2"
		enc := json.NewEncoder(w)
		switch r.URL.Path {
		case "/oauth/device/code":
			_ = enc.Encode(DeviceCode{DeviceCode: "dev", UserCode: "ABCD1234", VerificationURL: "https://trakt.tv/activate", ExpiresIn: 600, Interval: 1})
		case "/oauth/device/token":
			if f.polls.Add(1) < 2 {
				w.WriteHeader(http.StatusBadRequest) // pending
				return
			}
			_ = enc.Encode(Token{AccessToken: "access-1", RefreshToken: "refresh-1", ExpiresIn: 3600, CreatedAt: time.Now().Unix()})
		case "/oauth/token":
			f.refresh.Add(1)
			_ = enc.Encode(Token{AccessToken: "access-2", RefreshToken: "refresh-2", ExpiresIn: 90 * 24 * 3600, CreatedAt: time.Now().Unix()})
		case "/users/settings":
			if !authed {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = enc.Encode(map[string]any{"user": map[string]any{"username": "ana", "vip": f.vip}})
		case "/sync/watchlist/movies/added":
			_ = enc.Encode([]map[string]any{
				{"movie": map[string]any{"ids": map[string]any{"imdb": "tt9999999"}}}, // not on any server
				{"movie": map[string]any{"ids": map[string]any{"imdb": "tt0816692"}}},
			})
		case "/sync/history":
			w.Header().Set("X-Pagination-Page-Count", "1")
			_ = enc.Encode([]map[string]any{
				{"type": "episode", "watched_at": "2026-11-12T20:00:00Z", "show": map[string]any{"ids": map[string]any{"imdb": "tt0944947"}}, "episode": map[string]any{"season": 1, "number": 7}},
				{"type": "movie", "watched_at": "2026-11-10T20:00:00Z", "movie": map[string]any{"ids": map[string]any{"imdb": "tt0816692"}}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

type world struct {
	svc  *Service
	accs *accounts.Store
	own  *plextest.Server
}

func newWorld(t *testing.T, vip bool) (*world, *fakeTrakt) {
	t.Helper()
	ft := &fakeTrakt{vip: vip}
	api := ft.serve(t)
	t.Cleanup(api.Close)
	movie := plex.Metadata{RatingKey: "10", Type: "movie", Title: "Interstellar", Guids: []plex.Guid{{ID: "imdb://tt0816692"}},
		Media: []plex.Media{{Parts: []plex.Part{{Key: "/library/parts/10/1/f.mkv"}}}}}
	show := plex.Metadata{RatingKey: "300", Type: "show", Title: "GoT", Guids: []plex.Guid{{ID: "imdb://tt0944947"}}}
	ep := plex.Metadata{RatingKey: "301", Type: "episode", GrandparentRatingKey: "300", ParentIndex: 1, Index: 7,
		Media: []plex.Media{{Parts: []plex.Part{{Key: "/library/parts/301/1/f.mkv"}}}}}
	own := (&plextest.Server{MachineID: "own", Token: "own-token",
		Sections: []plextest.Section{{Key: "1", Type: "movie", Items: []plex.Metadata{movie}}, {Key: "2", Type: "show", Shows: []plex.Metadata{show}, Items: []plex.Metadata{ep}}},
		Details:  map[string]plex.Metadata{"10": movie, "301": ep}}).Start()
	t.Cleanup(own.Close)
	tv := (&plextest.PlexTV{Token: "acc-token", Resources: []plex.Resource{{Name: "Casa", ClientIdentifier: "own", Provides: "server", Owned: true, AccessToken: "own-token",
		Connections: []plex.Connection{{URI: own.URL(), Local: true}}}}}).Start()
	t.Cleanup(tv.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	accs, _ := accounts.Open(filepath.Join(t.TempDir(), "a.json"), []byte("s"))
	_ = accs.Put(&accounts.Account{ID: "u", PlexToken: "acc-token", Admin: true})
	client := plex.NewClient("t", "t")
	reg := servers.NewRegistry(client, tv.URL(), func() []servers.Credential {
		return []servers.Credential{{AccountID: "u", Token: "acc-token"}}
	}, nil, time.Second, log)
	ctx := context.Background()
	if err := reg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	reg.Probe(ctx)
	store := index.NewStore()
	ix := &index.Indexer{Client: client, Store: store, Log: log, Timeout: time.Second, PageSize: 10, BatchSize: 10, Concurrency: 1}
	s := reg.Get("own")
	if _, err := ix.Sync(ctx, index.Source{MachineID: s.MachineID, Name: s.Name, Owned: true, API: s.API()}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Client: &Client{API: api.URL, ClientID: "cid", ClientSecret: "sec"}, Accounts: accs, Store: store, Registry: reg, Plex: client, Log: log}
	return &world{svc: svc, accs: accs, own: own}, ft
}

func (w *world) link(t *testing.T) error {
	t.Helper()
	ctx := context.Background()
	dc, err := w.svc.Client.StartDevice(ctx)
	if err != nil || dc.UserCode != "ABCD1234" {
		t.Fatalf("device code: %+v %v", dc, err)
	}
	if _, err := w.svc.Client.PollDevice(ctx, dc.DeviceCode); !errors.Is(err, ErrPending) {
		t.Fatalf("first poll should be pending: %v", err)
	}
	tok, err := w.svc.Client.PollDevice(ctx, dc.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.Link(ctx, "u", tok)
	return err
}

func TestOnlyVIPAccountsCanUseTrakt(t *testing.T) {
	w, _ := newWorld(t, false)
	if err := w.link(t); !errors.Is(err, ErrNotVIP) {
		t.Fatalf("a free Trakt account must be reported: %v", err)
	}
	if a := w.accs.Get("u"); w.svc.Available(a) || a.Prefs.Trakt.Username != "ana" {
		t.Fatalf("Trakt must stay unavailable without VIP: %+v", a.Prefs.Trakt)
	}
}

func TestWatchlistAndHistorySync(t *testing.T) {
	w, ft := newWorld(t, true)
	if err := w.link(t); err != nil {
		t.Fatal(err)
	}
	a := w.accs.Get("u")
	if !w.svc.Available(a) {
		t.Fatal("a VIP account must be able to use Trakt")
	}
	ids, err := w.svc.List(context.Background(), a, "movie", "watchlist")
	if err != nil || len(ids) != 2 || ids[1] != "tt0816692" {
		t.Fatalf("watchlist: %v %v", ids, err)
	}
	_, _ = w.accs.Update("u", func(a *accounts.Account) {
		a.Prefs.Trakt.SyncWatched = true
		a.Prefs.Trakt.ExpiresAt = time.Now().Add(time.Hour).Unix() // about to expire: must refresh
	})
	before := ft.refresh.Load()
	w.svc.SyncWatched(context.Background())
	if ft.refresh.Load() != before+1 || w.accs.Get("u").Prefs.Trakt.AccessToken != "access-2" {
		t.Fatal("an expiring token must be refreshed and saved")
	}
	if got := w.own.Scrobbled; len(got) != 2 || got[0] != "10" || got[1] != "301" {
		t.Fatalf("watched items must be marked in Plex, oldest first: %v", got)
	}
	if w.accs.Get("u").Prefs.Trakt.LastSync == 0 {
		t.Fatal("the last sync time must be saved")
	}
}
