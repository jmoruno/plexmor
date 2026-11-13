package stremio

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jmoruno/plexmor/internal/accounts"
)

// Paths the proxy may forward to Plex: the files of the videos, external subtitles and the
// HLS transcoder. Anything else is refused, so the proxy cannot be used to drive the server.
var playPrefixes = []string{"/library/parts/", "/library/streams/", "/video/:/transcode/universal/"}

func newProxyClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 16
	t.ResponseHeaderTimeout = 60 * time.Second
	t.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	return &http.Client{
		Transport: t,
		// Plex redirects are followed by the client itself; never across hosts with our token.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// copied response headers.
var playHeaders = []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag", "Content-Disposition"}

// play forwards a video, subtitle or HLS request to Plex with the account's token. The client
// never sees the token, and only needs to reach Plexmor (it works away from home).
func (h *Handler) play(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	plexPath := "/" + r.PathValue("path")
	if !allowedPlay(plexPath) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	srv := h.Registry.Get(r.PathValue("server"))
	if srv == nil || srv.TokenFor(a.ID) == "" || !a.ServerAllowed(srv.MachineID) {
		http.NotFound(w, r)
		return
	}
	if !srv.Healthy() {
		http.Error(w, "server unreachable", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	for k := range q {
		if strings.EqualFold(k, "X-Plex-Token") {
			q.Del(k)
		}
	}
	target := strings.TrimRight(srv.Best.URI, "/") + plexPath
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, nil)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	for _, k := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since", "User-Agent"} {
		if v := r.Header.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("X-Plex-Token", srv.TokenFor(a.ID))
	req.Header.Set("X-Plex-Product", "Plexmor")
	if q.Get("X-Plex-Client-Identifier") == "" {
		req.Header.Set("X-Plex-Client-Identifier", h.Client.ClientIdentifier)
	}
	resp, err := h.proxyClient.Do(req)
	if err != nil {
		if r.Context().Err() == nil {
			h.Log.Warn("proxy request failed", "server", srv.Label(), "error", err)
			http.Error(w, "plex unreachable", http.StatusBadGateway)
		}
		return
	}
	defer resp.Body.Close()

	// A video can play for hours: lift the server's write timeout for this response.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	hdr := w.Header()
	for _, k := range playHeaders {
		if v := resp.Header.Get(k); v != "" {
			hdr.Set(k, v)
		}
	}
	hdr.Set("Cache-Control", "private, no-transform")
	if isPlaylist(resp.Header.Get("Content-Type"), plexPath) && resp.StatusCode == http.StatusOK {
		// Plex playlists may point to absolute /video/... paths: keep them inside the proxy.
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			http.Error(w, "plex error", http.StatusBadGateway)
			return
		}
		body = rewritePlaylist(body, "/"+a.ID+"/play/"+srv.MachineID)
		hdr.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		return
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.CopyBuffer(w, resp.Body, make([]byte, 256<<10))
	}
}

func allowedPlay(p string) bool {
	if strings.Contains(p, "..") {
		return false
	}
	for _, prefix := range playPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func isPlaylist(contentType, p string) bool {
	return strings.Contains(strings.ToLower(contentType), "mpegurl") || strings.HasSuffix(p, ".m3u8")
}

// rewritePlaylist prefixes absolute paths in an HLS playlist so they go through the proxy.
func rewritePlaylist(body []byte, prefix string) []byte {
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "/") {
			line = prefix + line
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// image serves a Plex poster or background resized by Plex.
func (h *Handler) image(w http.ResponseWriter, r *http.Request, a *accounts.Account) {
	plexPath := "/" + r.PathValue("path")
	if !strings.HasPrefix(plexPath, "/library/metadata/") || strings.Contains(plexPath, "..") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	srv := h.Registry.Get(r.PathValue("server"))
	if srv == nil || srv.TokenFor(a.ID) == "" || !a.ServerAllowed(srv.MachineID) || !srv.Healthy() {
		http.NotFound(w, r)
		return
	}
	width, _ := strconv.Atoi(r.URL.Query().Get("w"))
	height, _ := strconv.Atoi(r.URL.Query().Get("h"))
	width, height = clamp(width, 300), clamp(height, 450)
	q := url.Values{
		"width": {strconv.Itoa(width)}, "height": {strconv.Itoa(height)},
		"minSize": {"1"}, "upscale": {"1"}, "url": {plexPath},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(srv.Best.URI, "/")+"/photo/:/transcode?"+q.Encode(), nil)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req.Header.Set("X-Plex-Token", srv.TokenFor(a.ID))
	req.Header.Set("X-Plex-Client-Identifier", h.Client.ClientIdentifier)
	resp, err := h.proxyClient.Do(req)
	if err != nil {
		http.Error(w, "plex unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "image not available", resp.StatusCode)
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "public, max-age=604800")
	_, _ = io.Copy(w, resp.Body)
}

func clamp(v, def int) int {
	if v <= 0 {
		return def
	}
	return min(v, 1920)
}
