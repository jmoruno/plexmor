package stremio

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/index"
)

// Meta is the detail page of a title that Cinemeta does not know (no IMDb id).
type Meta struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Poster      string   `json:"poster,omitempty"`
	PosterShape string   `json:"posterShape,omitempty"`
	Background  string   `json:"background,omitempty"`
	Description string   `json:"description,omitempty"`
	ReleaseInfo string   `json:"releaseInfo,omitempty"`
	Runtime     string   `json:"runtime,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Videos      []Video  `json:"videos,omitempty"`
}

// Video is an episode of a series.
type Video struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Season    int    `json:"season"`
	Episode   int    `json:"episode"`
	Released  string `json:"released,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
	Overview  string `json:"overview,omitempty"`
}

func (h *Handler) meta(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	id := strings.TrimSuffix(r.PathValue("id"), ".json")
	server, rk, ok := index.ParsePlexmorID(id)
	if !ok || h.Registry.Get(server) == nil || h.Registry.Get(server).TokenFor(a.ID) == "" {
		writeJSON(w, http.StatusOK, "private, max-age=60", map[string]any{"meta": nil})
		return
	}
	base := h.BaseURL(r)
	var m *Meta
	if it := h.Store.Item(server, rk); it != nil && it.Type == "movie" {
		m = &Meta{
			ID: id, Type: "movie", Name: it.Title, PosterShape: "poster",
			Poster:      h.imageURL(base, a, server, it.Thumb, 300, 450),
			Background:  h.imageURL(base, a, server, it.Art, 1280, 720),
			Description: it.Summary, Genres: it.Genres,
		}
		if it.Year > 0 {
			m.ReleaseInfo = strconv.Itoa(it.Year)
		}
		if it.Duration > 0 {
			m.Runtime = strconv.FormatInt(it.Duration/60000, 10) + " min"
		}
	} else if sh := h.Store.Show(server, rk); sh != nil {
		m = &Meta{
			ID: id, Type: "series", Name: sh.Title, PosterShape: "poster",
			Poster:      h.imageURL(base, a, server, sh.Thumb, 300, 450),
			Background:  h.imageURL(base, a, server, sh.Art, 1280, 720),
			Description: sh.Summary, Genres: sh.Genres,
		}
		if sh.Year > 0 {
			m.ReleaseInfo = strconv.Itoa(sh.Year)
		}
		for _, ep := range h.Store.Episodes(server, rk) {
			v := Video{
				ID: index.PlexmorID(server, ep.RatingKey), Title: ep.Title, Season: ep.Season, Episode: ep.Episode,
				Thumbnail: h.imageURL(base, a, server, ep.Thumb, 400, 225), Overview: ep.Summary,
			}
			if ep.Released != "" {
				v.Released = ep.Released + "T00:00:00.000Z"
			}
			m.Videos = append(m.Videos, v)
		}
	}
	writeJSON(w, http.StatusOK, "private, max-age=300", map[string]any{"meta": m})
}
