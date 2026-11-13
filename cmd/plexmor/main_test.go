package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	if err := health(addr); err != nil {
		t.Fatalf("healthy server: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := health(addr); err == nil {
		t.Fatal("an error status must fail the healthcheck")
	}
	if err := health("127.0.0.1:1"); err == nil {
		t.Fatal("nothing listening must fail the healthcheck")
	}
	if err := health("no-port"); err == nil {
		t.Fatal("a bad LISTEN must fail")
	}
}
