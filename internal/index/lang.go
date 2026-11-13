package index

import (
	"strings"

	"github.com/jmoruno/plexmor/internal/plex"
)

// Canonical language keys. Spanish is split into Castilian and Latin American Spanish
// because for the user they are different languages.
const (
	Castellano = "castellano"
	Latino     = "latino"
	Unknown    = "desconocido"
)

var byISO = map[string]string{
	"en": "ingles", "eng": "ingles",
	"ja": "japones", "jpn": "japones",
	"fr": "frances", "fra": "frances", "fre": "frances",
	"it": "italiano", "ita": "italiano",
	"de": "aleman", "deu": "aleman", "ger": "aleman",
	"pt": "portugues", "por": "portugues",
	"ca": "catalan", "cat": "catalan",
	"eu": "euskera", "eus": "euskera", "baq": "euskera",
	"gl": "gallego", "glg": "gallego",
	"ko": "coreano", "kor": "coreano",
	"zh": "chino", "zho": "chino", "chi": "chino",
	"ru": "ruso", "rus": "ruso",
	"nl": "neerlandes", "nld": "neerlandes", "dut": "neerlandes",
	"pl": "polaco", "pol": "polaco",
	"hi": "hindi", "hin": "hindi",
	"ar": "arabe", "ara": "arabe",
	"tr": "turco", "tur": "turco",
	"sv": "sueco", "swe": "sueco",
	"da": "danes", "dan": "danes",
	"no": "noruego", "nor": "noruego", "nb": "noruego", "nob": "noruego",
	"fi": "finlandes", "fin": "finlandes",
}

// Display names for the canonical keys.
var display = map[string]string{
	Castellano: "Castellano", Latino: "Latino", "ingles": "Inglés", "japones": "Japonés",
	"frances": "Francés", "italiano": "Italiano", "aleman": "Alemán", "portugues": "Portugués",
	"catalan": "Catalán", "euskera": "Euskera", "gallego": "Gallego", "coreano": "Coreano",
	"chino": "Chino", "ruso": "Ruso", "neerlandes": "Neerlandés", "polaco": "Polaco",
	"hindi": "Hindi", "arabe": "Árabe", "turco": "Turco", "sueco": "Sueco", "danes": "Danés",
	"noruego": "Noruego", "finlandes": "Finlandés", Unknown: "Desconocido",
}

// English display names for the canonical keys.
var displayEN = map[string]string{
	Castellano: "Spanish (Spain)", Latino: "Spanish (Latin America)", "ingles": "English", "japones": "Japanese",
	"frances": "French", "italiano": "Italian", "aleman": "German", "portugues": "Portuguese",
	"catalan": "Catalan", "euskera": "Basque", "gallego": "Galician", "coreano": "Korean",
	"chino": "Chinese", "ruso": "Russian", "neerlandes": "Dutch", "polaco": "Polish",
	"hindi": "Hindi", "arabe": "Arabic", "turco": "Turkish", "sueco": "Swedish", "danes": "Danish",
	"noruego": "Norwegian", "finlandes": "Finnish", Unknown: "Unknown",
}

// byEnglish maps English language names to the canonical keys.
var byEnglish = map[string]string{
	"castilian": Castellano, "spanish (spain)": Castellano, "latin": Latino, "latin american": Latino,
	"latin american spanish": Latino, "spanish (latin america)": Latino, "unknown": Unknown,
}

func init() {
	for key, name := range displayEN {
		if _, taken := byEnglish[strings.ToLower(name)]; !taken {
			byEnglish[strings.ToLower(name)] = key
		}
	}
}

// latinHints in a track title mean Latin American Spanish.
var latinHints = []string{"latino", "latam", "latinoam", "lat.", "mexic", "argentin", "colombia", "chile", "venezuel", "peru", "es-419", "neutro"}

// castilianHints in a track title mean Castilian Spanish.
var castilianHints = []string{"castellano", "españa", "espana", "spain", "es-es", "castilian", "european"}

// Latin American locales in BCP 47 tags.
var latinRegions = map[string]bool{
	"419": true, "mx": true, "ar": true, "co": true, "cl": true, "pe": true, "ve": true, "us": true,
	"ec": true, "gt": true, "cu": true, "bo": true, "do": true, "hn": true, "py": true, "sv": true,
	"ni": true, "cr": true, "pr": true, "pa": true, "uy": true,
}

// Language returns the canonical language of a track.
func Language(s plex.Stream) string {
	tag := strings.ToLower(strings.TrimSpace(s.LanguageTag))
	code := strings.ToLower(strings.TrimSpace(s.LanguageCode))
	base, region, _ := strings.Cut(strings.ReplaceAll(tag, "_", "-"), "-")
	if base == "" {
		base = code
	}
	text := strings.ToLower(s.Title + " " + s.DisplayTitle + " " + s.ExtendedDisplayTitle)
	if base == "es" || base == "spa" || code == "spa" {
		switch {
		case containsAny(text, latinHints):
			return Latino
		case containsAny(text, castilianHints):
			return Castellano
		case latinRegions[region]:
			return Latino
		default:
			return Castellano
		}
	}
	if name, ok := byISO[base]; ok {
		return name
	}
	if name, ok := byISO[code]; ok {
		return name
	}
	if l := strings.ToLower(strings.TrimSpace(s.Language)); l != "" {
		return l
	}
	return Unknown
}

// DisplayLanguage returns the human name of a canonical language.
func DisplayLanguage(key string) string {
	if name, ok := display[key]; ok {
		return name
	}
	if key == "" {
		return display[Unknown]
	}
	return strings.ToUpper(key[:1]) + key[1:]
}

// LanguageName returns the human name of a canonical language in the interface language ("es" or "en").
func LanguageName(key, lang string) string {
	if lang == "en" {
		if name, ok := displayEN[key]; ok {
			return name
		}
		if key == "" {
			return displayEN[Unknown]
		}
	}
	return DisplayLanguage(key)
}

// NormalizeLanguage maps user input (castellano, Castellano, es-ES, inglés, English...) to a canonical key.
func NormalizeLanguage(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	replacer := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ñ", "n")
	v = replacer.Replace(v)
	switch v {
	case "es", "es-es", "spa", "espanol", "spanish":
		return Castellano
	case "es-419", "es-mx", "lat", "latam":
		return Latino
	}
	if name, ok := byISO[v]; ok {
		return name
	}
	if name, ok := byEnglish[v]; ok {
		return name
	}
	return v
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// KnownLanguages lists the canonical language keys with a display name, most common first.
func KnownLanguages() []string {
	return []string{Castellano, Latino, "ingles", "catalan", "euskera", "gallego", "frances", "italiano", "aleman",
		"portugues", "japones", "coreano", "chino", "ruso", "neerlandes", "polaco", "hindi", "arabe", "turco",
		"sueco", "danes", "noruego", "finlandes"}
}
