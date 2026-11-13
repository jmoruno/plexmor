// Package httpsec holds the HTTP protections shared by every Plexmor route.
package httpsec

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Headers adds the security headers every response gets. The panel adds its own
// Content-Security-Policy on top.
func Headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		// Addon URLs carry the account key: never leak them in a Referer.
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// Protected only lets through requests with "Authorization: Bearer <token>" (when token is
// set) or those allow accepts, such as a signed-in administrator.
func Protected(next http.Handler, token string, allow func(*http.Request) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			if got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok &&
				subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}
		if allow != nil && allow(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="plexmor"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}
