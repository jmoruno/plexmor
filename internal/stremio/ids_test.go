package stremio

import (
	"reflect"
	"testing"
)

func TestLookupKeys(t *testing.T) {
	cases := []struct {
		kind, id string
		want     []string
	}{
		{"movie", "tt0816692", []string{"imdb:tt0816692"}},
		{"movie", "tmdb:157336", []string{"tmdb:157336"}},
		{"series", "tt0944947:1:5", []string{"imdb:tt0944947:1:5"}},
		{"series", "tmdb:1399:2:10", []string{"tmdb:1399:2:10"}},
		{"series", "tt0944947", nil},     // a series needs season and episode
		{"series", "tt0944947:x:5", nil}, // malformed
		{"movie", "tt0816692:1:1", nil},  // a movie has no episode
		{"movie", "kitsu:123", nil},      // unsupported prefix
	}
	for _, c := range cases {
		if got := lookupKeys(c.kind, c.id); !reflect.DeepEqual(got, c.want) {
			t.Errorf("lookupKeys(%q, %q) = %v, want %v", c.kind, c.id, got, c.want)
		}
	}
}
