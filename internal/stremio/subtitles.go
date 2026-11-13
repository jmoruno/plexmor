package stremio

import (
	"net/http"
	"strings"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/index"
)

// Subtitle is a Stremio subtitle.
type Subtitle struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Lang string `json:"lang"`
}

// iso639 maps the canonical language keys to the ISO 639-2 codes Stremio shows.
var iso639 = map[string]string{
	index.Castellano: "spa", index.Latino: "spa", "ingles": "eng", "japones": "jpn", "frances": "fre",
	"italiano": "ita", "aleman": "ger", "portugues": "por", "catalan": "cat", "euskera": "baq",
	"gallego": "glg", "coreano": "kor", "chino": "chi", "ruso": "rus", "neerlandes": "dut",
	"polaco": "pol", "hindi": "hin", "arabe": "ara", "turco": "tur", "sueco": "swe", "danes": "dan",
	"noruego": "nor", "finlandes": "fin",
}

// textSubtitle reports whether a subtitle codec is a text format Stremio can load.
func textSubtitle(codec string) bool {
	switch strings.ToLower(codec) {
	case "srt", "subrip", "vtt", "webvtt":
		return true
	}
	return false
}

// subtitles offers the external subtitle files Plex has next to the video. They always go
// through Plexmor, because Plex needs the token to serve them.
func (h *Handler) subtitles(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	kind := r.PathValue("type")
	id := strings.TrimSuffix(r.PathValue("id"), ".json")
	base := h.BaseURL(r)
	out := []Subtitle{}
	seen := map[string]bool{}
	for _, it := range h.items(kind, id) {
		srv := h.Registry.Get(it.Server)
		if srv == nil || !srv.Healthy() || srv.TokenFor(a.ID) == "" || !a.SectionAllowed(it.Server, it.Section) {
			continue
		}
		for _, v := range it.Versions {
			for _, s := range v.Subtitles {
				if s.Key == "" || !textSubtitle(s.Codec) || seen[it.Server+s.Key] {
					continue
				}
				seen[it.Server+s.Key] = true
				lang := iso639[s.Language]
				if lang == "" {
					lang = s.Language
				}
				out = append(out, Subtitle{
					ID:   "plexmor-" + it.Server + "-" + strings.TrimPrefix(s.Key, "/library/streams/"),
					URL:  PlayURL(base, a.ID, it.Server, s.Key),
					Lang: lang,
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, "private, max-age=300", map[string]any{"subtitles": out})
}
