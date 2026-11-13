package panel

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/i18n"
	"github.com/jmoruno/plexmor/internal/index"
)

// TestPreview writes the pages with sample data to PLEXMOR_PREVIEW (a folder) for visual review.
func TestPreview(t *testing.T) {
	dir := os.Getenv("PLEXMOR_PREVIEW")
	if dir == "" {
		t.Skip("set PLEXMOR_PREVIEW to a folder to render the pages")
	}
	p := &Panel{}
	p.Routes(newMux())
	acc := &accounts.Account{ID: "x", Username: "admin", Admin: true, Prefs: accounts.Prefs{StreamMode: "auto", MaxStreams: 0}}
	for _, lang := range []string{"es", "en"} {
		pg := page{
			Lang: lang, Nonce: "n", Version: "v0.3.0", Account: acc,
			ManifestURL: "https://plexmor.example.com/3f9c0b7e1d2a4c5b8e6f7a9d0c1b2e3f/manifest.json",
			StremioURL:  "stremio://plexmor.example.com/3f9c.../manifest.json", WebURL: "#",
			Langs:       []langView{{"castellano", index.LanguageName("castellano", lang)}, {"latino", index.LanguageName("latino", lang)}, {"ingles", index.LanguageName("ingles", lang)}},
			LangOptions: []langView{{"catalan", index.LanguageName("catalan", lang)}, {"japones", index.LanguageName("japones", lang)}},
			Mode:        "auto", Items: 1652,
			Resolutions: map[string]bool{"4k": true, "1440": true, "1080": true, "720": true, "sd": true},
			Servers: []serverView{
				{MachineID: "a", Name: "Casa", Healthy: true, Enabled: true, Connection: i18n.T(lang, "conn.local"), LatencyMs: 22, Items: 1652, Synced: i18n.T(lang, "ago.min", "4"),
					Sections: []sectionView{{"1", "Movies", i18n.T(lang, "kind.movie"), true}, {"2", "TV", i18n.T(lang, "kind.show"), true}, {"3", "Conciertos", i18n.T(lang, "kind.movie"), true}}},
				{MachineID: "b", Name: "Biblioteca", Owner: "amigo", Healthy: false, Enabled: true, Error: "no connections"},
			},
			Users: []userView{{ID: "x", Name: "admin", Admin: true, Me: true, LastSeen: i18n.T(lang, "ago.now")}, {ID: "y", Name: "amigo", LastSeen: i18n.T(lang, "ago.hours", "2")}},
		}
		var home, login bytes.Buffer
		if err := p.tmpl[lang].ExecuteTemplate(&home, "home.html", pg); err != nil {
			t.Fatal(err)
		}
		if err := p.tmpl[lang].ExecuteTemplate(&login, "login.html", page{Lang: lang, Nonce: "n", Version: "v0.3.0"}); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, "home."+lang+".html"), home.Bytes(), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "login."+lang+".html"), login.Bytes(), 0o644)
	}
}

func newMux() *http.ServeMux { return http.NewServeMux() }
