package stremio

import (
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/index"
	"github.com/jmoruno/plexmor/internal/plex"
	"github.com/jmoruno/plexmor/internal/servers"
)

// testAcc is the account used by the tests; its plex.tv token is "account".
const testAcc = "acc"

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newAccounts(t testing.TB, mode string) *accounts.Store {
	t.Helper()
	s, err := accounts.Open(filepath.Join(t.TempDir(), "accounts.json"), []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(&accounts.Account{ID: testAcc, PlexToken: "account", Admin: true, Username: "ana", Prefs: accounts.Prefs{StreamMode: mode}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func credentials(s *accounts.Store) func() []servers.Credential {
	return func() []servers.Credential {
		var out []servers.Credential
		for _, a := range s.All() {
			out = append(out, servers.Credential{AccountID: a.ID, Token: a.PlexToken})
		}
		return out
	}
}

func newHandler(store *index.Store, reg *servers.Registry, accs *accounts.Store, langs []string) (*Handler, http.Handler) {
	h := &Handler{Store: store, Registry: reg, Accounts: accs, Client: plex.NewClient("test-client", "test"),
		Languages: langs, Version: "v1.2.3", Log: discard()}
	mux := http.NewServeMux()
	h.Routes(mux)
	return h, CORS(mux)
}

// accPath puts a test path under the test account.
func accPath(p string) string { return "/" + testAcc + p }
