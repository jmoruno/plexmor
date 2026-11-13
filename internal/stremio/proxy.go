package stremio

import (
	"bufio"
	"bytes"
	"fmt"
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
	// Reverse proxies such as Nginx would otherwise store the whole video on disk while the
	// player reads it, stall when their temporary file is full, and make Plex drop the stream.
	hdr.Set("X-Accel-Buffering", "no")
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
		h.relay(w, r, req, resp, srv.Label())
	}
}

// maxResumes is how many times one response may be resumed after Plex drops it, waiting
// resumeDelay longer each time.
const maxResumes = 5

var resumeDelay = 500 * time.Millisecond

// relay copies a Plex response to the client. When Plex drops the connection in the middle of
// a file, it asks again from the byte it got to and carries on, so the player never notices.
func (h *Handler) relay(w http.ResponseWriter, r *http.Request, req *http.Request, resp *http.Response, server string) {
	start, end, resumable := byteSpan(resp)
	validator := resp.Header.Get("ETag")
	if validator == "" {
		validator = resp.Header.Get("Last-Modified")
	}
	body := resp.Body
	buf := make([]byte, 256<<10)
	var sent int64
	for attempt := 0; ; {
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				h.Log.Debug("proxy: the player closed the stream", "server", server, "bytes", sent)
				return
			}
			sent += int64(n)
		}
		if rerr == nil {
			continue
		}
		if rerr == io.EOF || r.Context().Err() != nil {
			return
		}
		if !resumable || attempt >= maxResumes || start+sent > end {
			h.Log.Warn("proxy: Plex closed the stream", "server", server, "bytes", sent, "error", rerr)
			return
		}
		attempt++
		h.Log.Warn("proxy: Plex closed the stream, resuming", "server", server, "bytes", sent, "attempt", attempt, "error", rerr)
		next, err := h.resume(r, req, start+sent, end, validator, attempt)
		if err != nil {
			h.Log.Warn("proxy: could not resume the stream", "server", server, "bytes", sent, "error", err)
			return
		}
		defer next.Close()
		body = next
	}
}

// resume asks Plex for the rest of a file, from byte from to byte to.
func (h *Handler) resume(r *http.Request, req *http.Request, from, to int64, validator string, attempt int) (io.ReadCloser, error) {
	select {
	case <-r.Context().Done():
		return nil, r.Context().Err()
	case <-time.After(time.Duration(attempt) * resumeDelay):
	}
	again := req.Clone(r.Context())
	again.Header.Set("Range", "bytes="+strconv.FormatInt(from, 10)+"-"+strconv.FormatInt(to, 10))
	again.Header.Del("If-None-Match")
	again.Header.Del("If-Modified-Since")
	if validator != "" {
		again.Header.Set("If-Range", validator) // only the same version of the file
	}
	resp, err := h.proxyClient.Do(again)
	if err != nil {
		return nil, err
	}
	if s, _, ok := byteSpan(resp); resp.StatusCode != http.StatusPartialContent || !ok || s != from {
		resp.Body.Close()
		return nil, fmt.Errorf("plex answered %d %q", resp.StatusCode, resp.Header.Get("Content-Range"))
	}
	return resp.Body, nil
}

// byteSpan is the byte range of the file a response carries: from Content-Range for 206, or
// the whole file for 200 with a known length.
func byteSpan(resp *http.Response) (start, end int64, ok bool) {
	switch resp.StatusCode {
	case http.StatusOK:
		if resp.ContentLength > 0 {
			return 0, resp.ContentLength - 1, true
		}
	case http.StatusPartialContent:
		cr, found := strings.CutPrefix(resp.Header.Get("Content-Range"), "bytes ")
		if !found {
			return 0, 0, false
		}
		span, _, _ := strings.Cut(cr, "/")
		a, b, found := strings.Cut(span, "-")
		s, err1 := strconv.ParseInt(a, 10, 64)
		e, err2 := strconv.ParseInt(b, 10, 64)
		if found && err1 == nil && err2 == nil && s <= e {
			return s, e, true
		}
	}
	return 0, 0, false
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
