package httpsec

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })

func TestHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	Headers(ok).ServeHTTP(rec, req)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers: %v", rec.Header())
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS only over HTTPS")
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	rec = httptest.NewRecorder()
	Headers(ok).ServeHTTP(rec, req)
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("HSTS behind an HTTPS proxy")
	}
}

func TestProtected(t *testing.T) {
	admin := func(r *http.Request) bool { return r.Header.Get("X-Admin") == "1" }
	for _, c := range []struct {
		token, auth, admin string
		want               int
	}{
		{"", "", "", http.StatusUnauthorized},
		{"", "Bearer ", "", http.StatusUnauthorized},
		{"", "", "1", http.StatusOK},
		{"s3cret", "Bearer s3cret", "", http.StatusOK},
		{"s3cret", "Bearer wrong", "", http.StatusUnauthorized},
		{"s3cret", "s3cret", "", http.StatusUnauthorized},
		{"s3cret", "", "1", http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		if c.admin != "" {
			req.Header.Set("X-Admin", c.admin)
		}
		rec := httptest.NewRecorder()
		Protected(ok, c.token, admin).ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%+v: got %d", c, rec.Code)
		}
	}
}
