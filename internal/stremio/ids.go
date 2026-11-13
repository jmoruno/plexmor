// Package stremio implements the Stremio addon protocol on top of the index.
package stremio

import (
	"strconv"
	"strings"

	"github.com/jmoruno/plexmor/internal/index"
)

// lookupKeys converts a Stremio id into index keys.
//
//	movie  tt0816692        -> imdb:tt0816692
//	movie  tmdb:157336      -> tmdb:157336
//	series tt0944947:1:5    -> imdb:tt0944947:1:5
//	series tmdb:1399:1:5    -> tmdb:1399:1:5
func lookupKeys(kind, id string) []string {
	id = strings.TrimSpace(id)
	var base, rest string
	switch {
	case strings.HasPrefix(id, "tt"):
		base, rest, _ = strings.Cut(id, ":")
		base = "imdb:" + base
	case strings.HasPrefix(id, "tmdb:"), strings.HasPrefix(id, "tvdb:"):
		scheme, after, _ := strings.Cut(id, ":")
		value, r, _ := strings.Cut(after, ":")
		base, rest = scheme+":"+value, r
	default:
		return nil
	}
	if kind != "series" {
		if rest != "" {
			return nil
		}
		return []string{base}
	}
	seasonStr, episodeStr, ok := strings.Cut(rest, ":")
	season, err1 := strconv.Atoi(seasonStr)
	episode, err2 := strconv.Atoi(episodeStr)
	if !ok || err1 != nil || err2 != nil {
		return nil
	}
	return []string{index.EpisodeKey(base, season, episode)}
}
