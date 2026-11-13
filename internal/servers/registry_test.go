package servers

import (
	"testing"
	"time"
)

func TestPickBestPrefersAddressesClientsCanReach(t *testing.T) {
	eps := []Endpoint{
		{URI: "docker", Address: "172.22.0.1", Local: true, Latency: 1 * time.Millisecond},
		{URI: "bridge", Address: "192.168.16.1", Local: true, Latency: 1 * time.Millisecond},
		{URI: "loopback", Address: "127.0.0.1", Local: true, Latency: 1 * time.Millisecond},
		{URI: "relay", Address: "1.2.3.4", Relay: true, Latency: 1 * time.Millisecond},
		{URI: "public", Address: "83.47.31.142", Latency: 5 * time.Millisecond},
		{URI: "lan", Address: "192.168.1.20", Local: true, Latency: 20 * time.Millisecond},
	}
	if got := pickBest(append([]Endpoint(nil), eps...)).URI; got != "lan" {
		t.Fatalf("got %s, want the home network address", got)
	}
	if got := pickBest(append([]Endpoint(nil), eps[:5]...)).URI; got != "public" {
		t.Fatalf("got %s, want the public address before a Docker bridge", got)
	}
	if got := pickBest(append([]Endpoint(nil), eps[:1]...)).URI; got != "docker" {
		t.Fatalf("a Docker address is still used when it is the only one, got %s", got)
	}
}
