package stremio

import "testing"

func TestEncodedEpisodeID(t *testing.T) {
	f := readyFixture(t)
	plain := get(t, f.handler, "/stream/series/tt0944947:1:7.json")
	encoded := get(t, f.handler, "/stream/series/tt0944947%3A1%3A7.json")
	if len(plain) == 0 || len(encoded) != len(plain) {
		t.Fatalf("Stremio sends ids URL-encoded: plain %d streams, encoded %d", len(plain), len(encoded))
	}
}
