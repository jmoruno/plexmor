// Package plextest provides fake plex.tv and Plex Media Server endpoints for tests.
package plextest

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoruno/plexmor/internal/plex"
)

// Section is a library of a fake server.
type Section struct {
	Key   string
	Type  string // movie or show
	Items []plex.Metadata
	// Shows are the series of a show section (type=2); Items holds its episodes (type=4).
	Shows []plex.Metadata
}

// Server is a fake Plex Media Server.
type Server struct {
	MachineID string
	Token     string
	// AlsoAccept is another token the server accepts (a token valid on several servers).
	AlsoAccept string
	// MoreTokens are the tokens of other users the server is shared with.
	MoreTokens []string
	Sections   []Section
	// Details by ratingKey, returned by /library/metadata (with tracks).
	Details map[string]plex.Metadata

	// OnDeck is answered by /library/onDeck.
	OnDeck []plex.Metadata
	// Scrobbled records the ratingKeys marked as watched.
	Scrobbled []string

	MetadataCalls atomic.Int64
	// FailSection makes the listing of that library answer 500.
	FailSection atomic.Value // string
	// FailMetadata makes every /library/metadata request answer 500.
	FailMetadata atomic.Bool
	// PartContent replaces the content of every file (default "CONTENT <path>").
	PartContent []byte
	// CutParts makes the next file responses stop after CutAfter bytes and close the
	// connection, as when Plex drops a stream.
	CutParts atomic.Int64
	CutAfter int
	// PartRequests counts the requests for files.
	PartRequests atomic.Int64
	srv          *httptest.Server
	mu           sync.Mutex
}

// Start launches the fake server.
func (s *Server) Start() *Server {
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// URL is the base URL of the fake server.
func (s *Server) URL() string { return s.srv.URL }

// Close stops the fake server.
func (s *Server) Close() { s.srv.Close() }

// SetDetail replaces the detailed metadata of an item (to simulate changes).
func (s *Server) SetDetail(m plex.Metadata) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Details[m.RatingKey] = m
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if tok := r.Header.Get("X-Plex-Token"); !s.accepts(tok) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/library/parts/") || strings.HasPrefix(path, "/library/streams/"):
		// Files are served whatever the Accept header, with Range support.
		s.PartRequests.Add(1)
		content := []byte("CONTENT " + path)
		if s.PartContent != nil {
			content = s.PartContent
		}
		if s.CutParts.Add(-1) >= 0 {
			w = &cutWriter{ResponseWriter: w, left: s.CutAfter}
		}
		http.ServeContent(w, r, "file", time.Unix(0, 0), bytes.NewReader(content))
		return
	case path == "/photo/:/transcode":
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("IMG " + r.URL.Query().Get("url")))
		return
	case path == "/video/:/transcode/universal/start.m3u8":
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=4000000\nsession/abc/base/index.m3u8\n/video/:/transcode/universal/session/abc/1/index.m3u8\n"))
		return
	}
	if r.Header.Get("Accept") != "application/json" {
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}
	switch {
	case path == "/:/scrobble":
		s.mu.Lock()
		s.Scrobbled = append(s.Scrobbled, r.URL.Query().Get("key"))
		s.mu.Unlock()
		writeMC(w, map[string]any{})
	case path == "/library/onDeck":
		writeMC(w, map[string]any{"size": len(s.OnDeck), "Metadata": s.OnDeck})
	case path == "/identity":
		writeMC(w, map[string]any{"machineIdentifier": s.MachineID, "version": "1.41.0"})
	case path == "/library/sections":
		var dirs []map[string]any
		for _, sec := range s.Sections {
			dirs = append(dirs, map[string]any{"key": sec.Key, "type": sec.Type, "title": sec.Type})
		}
		writeMC(w, map[string]any{"Directory": dirs})
	case strings.HasPrefix(path, "/library/sections/") && strings.HasSuffix(path, "/all"):
		key := strings.TrimSuffix(strings.TrimPrefix(path, "/library/sections/"), "/all")
		if fail, _ := s.FailSection.Load().(string); fail == key {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		s.serveAll(w, r, key)
	case strings.HasPrefix(path, "/library/metadata/"):
		s.MetadataCalls.Add(1)
		if s.FailMetadata.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		keys := strings.Split(strings.TrimPrefix(path, "/library/metadata/"), ",")
		s.mu.Lock()
		var out []plex.Metadata
		for _, k := range keys {
			if m, ok := s.Details[k]; ok {
				out = append(out, m)
			}
		}
		s.mu.Unlock()
		writeMC(w, map[string]any{"size": len(out), "Metadata": out})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) serveAll(w http.ResponseWriter, r *http.Request, key string) {
	var items []plex.Metadata
	for _, sec := range s.Sections {
		if sec.Key != key {
			continue
		}
		switch r.URL.Query().Get("type") {
		case "2":
			items = sec.Shows
		default:
			items = sec.Items
		}
	}
	// Listings never carry tracks, like the real server.
	listed := make([]plex.Metadata, len(items))
	for i, m := range items {
		listed[i] = stripTracks(m)
	}
	start, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Start"))
	size, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Size"))
	end := len(listed)
	if size > 0 && start+size < end {
		end = start + size
	}
	if start > len(listed) {
		start = len(listed)
	}
	writeMC(w, map[string]any{"size": end - start, "totalSize": len(listed), "Metadata": listed[start:end]})
}

func stripTracks(m plex.Metadata) plex.Metadata {
	media := make([]plex.Media, len(m.Media))
	for i, md := range m.Media {
		md.Parts = append([]plex.Part(nil), md.Parts...)
		for j := range md.Parts {
			md.Parts[j].Streams = nil
		}
		media[i] = md
	}
	m.Media = media
	return m
}

func writeMC(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"MediaContainer": v})
}

// PlexTV is a fake plex.tv: resources per token, PIN login and user details.
type PlexTV struct {
	Token     string
	Resources []plex.Resource
	// Accounts adds more tokens with their own resources and user.
	Accounts map[string]Account
	mu       sync.Mutex
	approved map[int64]string
	nextPin  int64
	srv      *httptest.Server
}

// Account is a plex.tv user of the fake.
type Account struct {
	User      plex.User
	Resources []plex.Resource
}

// LastPin is the id of the most recent PIN created.
func (p *PlexTV) LastPin() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nextPin
}

// Approve simulates the user signing in for a PIN with the given token.
func (p *PlexTV) Approve(pin int64, token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.approved == nil {
		p.approved = map[int64]string{}
	}
	p.approved[pin] = token
}

func (p *PlexTV) account(token string) (Account, bool) {
	if token == p.Token && token != "" {
		return Account{User: plex.User{UUID: "uuid-" + token, Username: "owner"}, Resources: p.Resources}, true
	}
	a, ok := p.Accounts[token]
	return a, ok
}

// Start launches the fake plex.tv.
func (p *PlexTV) Start() *PlexTV {
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte("<MediaContainer/>"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/pins":
			p.mu.Lock()
			p.nextPin++
			id := p.nextPin
			p.mu.Unlock()
			_ = json.NewEncoder(w).Encode(plex.Pin{ID: id, Code: "CODE" + strconv.FormatInt(id, 10)})
			return
		case strings.HasPrefix(r.URL.Path, "/api/v2/pins/"):
			id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v2/pins/"), 10, 64)
			p.mu.Lock()
			tok := p.approved[id]
			p.mu.Unlock()
			_ = json.NewEncoder(w).Encode(plex.Pin{ID: id, AuthToken: tok})
			return
		}
		acc, ok := p.account(r.Header.Get("X-Plex-Token"))
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v2/resources":
			_ = json.NewEncoder(w).Encode(acc.Resources)
		case "/api/v2/user":
			_ = json.NewEncoder(w).Encode(acc.User)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return p
}

// URL is the base URL of the fake plex.tv.
func (p *PlexTV) URL() string { return p.srv.URL }

// Close stops the fake plex.tv.
func (p *PlexTV) Close() { p.srv.Close() }

func (s *Server) accepts(tok string) bool {
	if tok == "" {
		return false
	}
	if tok == s.Token || tok == s.AlsoAccept {
		return true
	}
	for _, t := range s.MoreTokens {
		if t == tok {
			return true
		}
	}
	return false
}

// cutWriter sends left bytes of the body and then drops the connection.
type cutWriter struct {
	http.ResponseWriter
	left int
}

func (c *cutWriter) Write(p []byte) (int, error) {
	if len(p) <= c.left {
		c.left -= len(p)
		return c.ResponseWriter.Write(p)
	}
	n, _ := c.ResponseWriter.Write(p[:c.left])
	c.left = 0
	c.ResponseWriter.(http.Flusher).Flush()
	if conn, _, err := c.ResponseWriter.(http.Hijacker).Hijack(); err == nil {
		conn.Close()
	}
	return n, errors.New("connection dropped")
}
