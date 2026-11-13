package stremio

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jmoruno/plexmor/internal/index"
	"github.com/jmoruno/plexmor/internal/plex"
	"github.com/jmoruno/plexmor/internal/servers"
)

// BenchmarkStream measures a /stream request against an index of 30 000 items on three servers.
func BenchmarkStream(b *testing.B) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := index.NewStore()
	for s := 0; s < 3; s++ {
		sd := index.NewServerData(fmt.Sprintf("srv%d", s))
		for i := 0; i < 10000; i++ {
			key := fmt.Sprint(i)
			sd.Items[key] = &index.Item{
				Server: sd.MachineID, RatingKey: key, Type: "movie", Title: "Movie", UpdatedAt: 1,
				IDs: index.IDs{IMDb: fmt.Sprintf("tt%07d", i)},
				Versions: []index.Version{{
					PartKey: "/library/parts/" + key + "/1/file.mkv", Resolution: "1080", Size: 8 << 30, Detailed: true,
					Audio: []index.Audio{{Language: index.Castellano, Channels: 6}, {Language: "ingles", Channels: 8}},
				}},
			}
		}
		store.PutServer(sd)
	}
	accs := newAccounts(b, "")
	reg := servers.NewRegistry(plex.NewClient("bench", "bench"), "http://unused", credentials(accs), nil, time.Second, log)
	setHealthy(reg, "srv0", "srv1", "srv2")
	_, h := newHandler(store, reg, accs, []string{index.Castellano})
	req := httptest.NewRequest(http.MethodGet, accPath("/stream/movie/tt0004242.json"), nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatal(rec.Code)
		}
	}
}

func setHealthy(reg *servers.Registry, ids ...string) {
	var list []*servers.Server
	for _, id := range ids {
		list = append(list, &servers.Server{MachineID: id, Name: id, Owned: true, Token: "t", Tokens: map[string]string{testAcc: "t"},
			Best: &servers.Endpoint{URI: "http://" + id + ":32400", Local: true, Latency: time.Millisecond}})
	}
	reg.Set(list...)
}
