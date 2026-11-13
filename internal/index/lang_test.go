package index

import (
	"testing"

	"github.com/jmoruno/plexmor/internal/plex"
)

func TestLanguage(t *testing.T) {
	cases := []struct {
		name string
		s    plex.Stream
		want string
	}{
		{"plain es", plex.Stream{LanguageTag: "es"}, Castellano},
		{"es-ES", plex.Stream{LanguageTag: "es-ES"}, Castellano},
		{"es-419", plex.Stream{LanguageTag: "es-419"}, Latino},
		{"es-MX", plex.Stream{LanguageTag: "es-MX"}, Latino},
		{"spa code only", plex.Stream{LanguageCode: "spa"}, Castellano},
		{"title latino", plex.Stream{LanguageTag: "es", Title: "Latino"}, Latino},
		{"display latino", plex.Stream{LanguageCode: "spa", DisplayTitle: "Español (Latinoamérica) AC3 5.1"}, Latino},
		{"title castellano beats region", plex.Stream{LanguageTag: "es-MX", Title: "Castellano"}, Castellano},
		{"english", plex.Stream{LanguageTag: "en", LanguageCode: "eng"}, "ingles"},
		{"japanese code", plex.Stream{LanguageCode: "jpn"}, "japones"},
		{"catalan", plex.Stream{LanguageTag: "ca"}, "catalan"},
		{"unknown with name", plex.Stream{Language: "Klingon"}, "klingon"},
		{"nothing", plex.Stream{}, Unknown},
	}
	for _, c := range cases {
		if got := Language(c.s); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNormalizeLanguage(t *testing.T) {
	for in, want := range map[string]string{
		"Castellano": Castellano, "es-ES": Castellano, "español": Castellano,
		"latino": Latino, "es-419": Latino, "Inglés": "ingles", "en": "ingles", "jpn": "japones",
		"English": "ingles", "Japanese": "japones", "Latin American Spanish": Latino, "castilian": Castellano,
		"Spanish (Spain)": Castellano, "Dutch": "neerlandes",
	} {
		if got := NormalizeLanguage(in); got != want {
			t.Errorf("NormalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseGuids(t *testing.T) {
	ids := ParseGuids([]plex.Guid{{ID: "imdb://tt0816692"}, {ID: "tmdb://157336"}, {ID: "tvdb://81189"}, {ID: "plex://movie/5d77"}, {ID: "garbage"}})
	if ids != (IDs{IMDb: "tt0816692", TMDB: "157336", TVDB: "81189"}) {
		t.Fatalf("got %+v", ids)
	}
}

func TestLegacyGuids(t *testing.T) {
	cases := map[string]IDs{
		"com.plexapp.agents.imdb://tt0816692?lang=en":    {IMDb: "tt0816692"},
		"com.plexapp.agents.themoviedb://157336?lang=es": {TMDB: "157336"},
		"com.plexapp.agents.thetvdb://81189/1/5?lang=en": {TVDB: "81189"},
		"com.plexapp.agents.thetvdb://81189?lang=en":     {TVDB: "81189"},
		"com.plexapp.agents.hama://anidb-123?lang=en":    {},
		"plex://movie/5d7768":                            {},
	}
	for legacy, want := range cases {
		if got := ParseIDs(nil, legacy); got != want {
			t.Errorf("ParseIDs(%q) = %+v, want %+v", legacy, got, want)
		}
	}
	// Modern Guid entries win over the legacy guid.
	got := ParseIDs([]plex.Guid{{ID: "imdb://tt1"}}, "com.plexapp.agents.imdb://tt2?lang=en")
	if got.IMDb != "tt1" {
		t.Errorf("modern guid should win, got %+v", got)
	}
}

func TestLanguageName(t *testing.T) {
	for _, key := range KnownLanguages() {
		if LanguageName(key, "en") == DisplayLanguage(key) && key != "hindi" {
			t.Errorf("%s has no English name", key)
		}
	}
	if got := LanguageName(Latino, "en"); got != "Spanish (Latin America)" {
		t.Errorf("got %q", got)
	}
	if got := LanguageName(Latino, "es"); got != "Latino" {
		t.Errorf("got %q", got)
	}
}
