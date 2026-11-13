// Package servers keeps the list of reachable Plex servers and the best connection to each one.
package servers

import (
	"context"
	"log/slog"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoruno/plexmor/internal/plex"
)

// Endpoint is a working connection to a server.
type Endpoint struct {
	URI     string
	Address string // IP address reported by plex.tv
	Local   bool
	Relay   bool
	Latency time.Duration
}

// Kind classifies the endpoint: "local", "remote" or "relay" (translated for display).
func (e Endpoint) Kind() string {
	switch {
	case e.Relay:
		return "relay"
	case e.Local:
		return "local"
	default:
		return "remote"
	}
}

// Server is a Plex server visible to the account. Values are immutable once published.
type Server struct {
	MachineID    string
	Name         string
	Owner        string // empty for your own server
	Owned        bool
	OwnerAccount string            // the Plexmor account that owns the server, if any
	Token        string            // used to probe and index (the owner's when an account owns it)
	Tokens       map[string]string // access token of each account that can use the server
	Connections  []plex.Connection
	Best         *Endpoint // nil when no connection works
	LastError    string
	CheckedAt    time.Time
}

// Healthy reports whether the server can be used right now.
func (s *Server) Healthy() bool { return s.Best != nil }

// API returns the address and token to call the server with.
func (s *Server) API() plex.Server {
	if s.Best == nil {
		return plex.Server{Token: s.Token}
	}
	return plex.Server{URI: s.Best.URI, Token: s.Token}
}

// TokenFor returns the access token of an account on this server, or "".
func (s *Server) TokenFor(accountID string) string { return s.Tokens[accountID] }

// Label is the display name: your server by name, shared ones with their owner.
func (s *Server) Label() string {
	if s.Owned || s.Owner == "" {
		return s.Name
	}
	return s.Name + " (" + s.Owner + ")"
}

// Credential is a Plexmor account and its plex.tv token.
type Credential struct {
	AccountID string
	Token     string
}

// Registry discovers the servers of every account through plex.tv and probes their connections.
type Registry struct {
	client       *plex.Client
	plexTV       string
	creds        func() []Credential
	only         map[string]bool
	probeTimeout time.Duration
	log          *slog.Logger

	mu      sync.Mutex // serializes writers
	servers atomic.Pointer[map[string]*Server]
}

// NewRegistry creates a registry. creds lists the accounts; only limits the servers used by
// name (case-insensitive).
func NewRegistry(client *plex.Client, plexTV string, creds func() []Credential, only []string, probeTimeout time.Duration, log *slog.Logger) *Registry {
	r := &Registry{client: client, plexTV: plexTV, creds: creds, probeTimeout: probeTimeout, log: log}
	if len(only) > 0 {
		r.only = map[string]bool{}
		for _, n := range only {
			r.only[strings.ToLower(n)] = true
		}
	}
	empty := map[string]*Server{}
	r.servers.Store(&empty)
	return r
}

// Get returns a server by machine identifier, or nil.
func (r *Registry) Get(machineID string) *Server { return (*r.servers.Load())[machineID] }

// All returns every server sorted by name (own server first).
func (r *Registry) All() []*Server {
	m := *r.servers.Load()
	out := make([]*Server, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owned != out[j].Owned {
			return out[i].Owned
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// ForAccount returns the servers an account can use, sorted like All.
func (r *Registry) ForAccount(accountID string) []*Server {
	var out []*Server
	for _, s := range r.All() {
		if s.Tokens[accountID] != "" {
			out = append(out, s)
		}
	}
	return out
}

// Refresh reloads the server list of every account from plex.tv, keeping the probe results of
// known servers. An account whose plex.tv request fails keeps its previous servers.
func (r *Registry) Refresh(ctx context.Context) error {
	creds := r.creds()
	type result struct {
		cred      Credential
		resources []plex.Resource
		err       error
	}
	results := make([]result, len(creds))
	var wg sync.WaitGroup
	for i, c := range creds {
		wg.Add(1)
		go func(i int, c Credential) {
			defer wg.Done()
			res, err := r.client.Resources(ctx, r.plexTV, c.Token)
			results[i] = result{c, res, err}
		}(i, c)
	}
	wg.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()
	old := *r.servers.Load()
	next := map[string]*Server{}
	var firstErr error
	failed := 0
	for _, res := range results {
		if res.err != nil {
			failed++
			if firstErr == nil {
				firstErr = res.err
			}
			r.log.Warn("could not read the servers of an account", "account", res.cred.AccountID, "error", res.err)
			for _, s := range old { // keep what the account had
				if tok := s.Tokens[res.cred.AccountID]; tok != "" {
					merge(next, old, s.withOnly(res.cred.AccountID, tok))
				}
			}
			continue
		}
		for _, rs := range res.resources {
			if r.only != nil && !r.only[strings.ToLower(rs.Name)] {
				continue
			}
			s := &Server{
				MachineID:   rs.ClientIdentifier,
				Name:        rs.Name,
				Owned:       rs.Owned,
				Token:       rs.AccessToken,
				Tokens:      map[string]string{res.cred.AccountID: rs.AccessToken},
				Connections: rs.Connections,
			}
			if rs.Owned {
				s.OwnerAccount = res.cred.AccountID
			} else {
				s.Owner = rs.SourceTitle
			}
			merge(next, old, s)
		}
	}
	if len(creds) > 0 && failed == len(creds) {
		return firstErr
	}
	r.servers.Store(&next)
	r.log.Info("servers refreshed", "servers", len(next), "accounts", len(creds))
	return nil
}

// withOnly returns a copy of s that only carries one account.
func (s *Server) withOnly(accountID, token string) *Server {
	cp := *s
	cp.Tokens = map[string]string{accountID: token}
	return &cp
}

// merge adds the view of one account to the server list. The owner's view wins for the name,
// the indexing token and the connections (it lists the local addresses too).
func merge(next, old map[string]*Server, s *Server) {
	cur, ok := next[s.MachineID]
	if !ok {
		if prev, ok := old[s.MachineID]; ok {
			s.Best, s.LastError, s.CheckedAt = prev.Best, prev.LastError, prev.CheckedAt
		}
		next[s.MachineID] = s
		return
	}
	cp := *cur
	cp.Tokens = make(map[string]string, len(cur.Tokens)+len(s.Tokens))
	for k, v := range cur.Tokens {
		cp.Tokens[k] = v
	}
	for k, v := range s.Tokens {
		cp.Tokens[k] = v
	}
	if s.Owned && !cur.Owned {
		cp.Name, cp.Owned, cp.Owner, cp.Token, cp.Connections = s.Name, true, "", s.Token, s.Connections
		cp.OwnerAccount = s.OwnerAccount
	}
	next[s.MachineID] = &cp
}

type probeResult struct {
	endpoint Endpoint
	err      error
}

// Probe tests every connection of every server in parallel and records the best one.
func (r *Registry) Probe(ctx context.Context) {
	current := *r.servers.Load()
	results := make(map[string]*Server, len(current))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for id, s := range current {
		wg.Add(1)
		go func(id string, s *Server) {
			defer wg.Done()
			updated := r.probeServer(ctx, s)
			mu.Lock()
			results[id] = updated
			mu.Unlock()
		}(id, s)
	}
	wg.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()
	latest := *r.servers.Load()
	next := make(map[string]*Server, len(latest))
	for id, s := range latest {
		if probed, ok := results[id]; ok {
			cp := *s
			cp.Best, cp.LastError, cp.CheckedAt = probed.Best, probed.LastError, probed.CheckedAt
			s = &cp
		}
		next[id] = s
	}
	r.servers.Store(&next)
}

func (r *Registry) probeServer(ctx context.Context, s *Server) *Server {
	ch := make(chan probeResult, len(s.Connections))
	for _, c := range s.Connections {
		go func(c plex.Connection) {
			pctx, cancel := context.WithTimeout(ctx, r.probeTimeout)
			defer cancel()
			start := time.Now()
			id, err := r.client.Identity(pctx, plex.Server{URI: c.URI, Token: s.Token})
			if err == nil && id.MachineIdentifier != "" && id.MachineIdentifier != s.MachineID {
				// A local address of a friend's server can point to another server in our own network.
				err = errWrongServer
			}
			ch <- probeResult{Endpoint{URI: c.URI, Address: address(c), Local: c.Local, Relay: c.Relay, Latency: time.Since(start)}, err}
		}(c)
	}
	var ok []Endpoint
	var lastErr error
	for range s.Connections {
		res := <-ch
		if res.err != nil {
			lastErr = res.err
			continue
		}
		ok = append(ok, res.endpoint)
	}
	cp := *s
	cp.CheckedAt = time.Now()
	cp.Best = pickBest(ok)
	cp.LastError = ""
	if cp.Best == nil {
		if lastErr != nil {
			cp.LastError = lastErr.Error()
		} else {
			cp.LastError = "no connections"
		}
		if s.Best != nil {
			r.log.Warn("server unreachable", "server", s.Label(), "error", cp.LastError)
		}
	} else if s.Best == nil || s.Best.URI != cp.Best.URI {
		r.log.Info("server connection", "server", s.Label(), "kind", cp.Best.Kind(), "address", cp.Best.Address, "latency", cp.Best.Latency.Round(time.Millisecond))
	}
	return &cp
}

// pickBest prefers direct connections over the relay (bandwidth-limited), then the lowest latency.
func pickBest(eps []Endpoint) *Endpoint {
	if len(eps) == 0 {
		return nil
	}
	sort.SliceStable(eps, func(i, j int) bool {
		if ci, cj := reachClass(eps[i]), reachClass(eps[j]); ci != cj {
			return ci < cj
		}
		return eps[i].Latency < eps[j].Latency
	})
	best := eps[0]
	return &best
}

// HomeOnly reports whether only devices in the server's own network can use this connection
// (a private, Docker or loopback address), so a client away from home needs the proxy.
func (e Endpoint) HomeOnly() bool {
	c := reachClass(e)
	return c == 0 || c == 2 || c == 3
}

// reachClass orders connections by how likely the Stremio client (not Plexmor) can use them.
// Plexmor usually runs in Docker next to Plex, so it also reaches addresses no client can:
// Plex lists every interface of its host, Docker bridges included (172.17.0.1, 192.168.16.1...).
// A bridge is the gateway of its network, so it ends in .1, while the Plex machine itself rarely does.
//
//	0 home network (192.168.x, 10.x, IPv6 ULA)
//	1 public address
//	2 172.16.0.0/12 or a private address ending in .1, almost always a Docker bridge
//	3 loopback and link-local, only reachable from the Plex host itself
//	4 Plex relay, capped at a few Mbps
func reachClass(e Endpoint) int {
	if e.Relay {
		return 4
	}
	ip, err := netip.ParseAddr(e.Address)
	if err != nil {
		return 1
	}
	ip = ip.Unmap()
	switch {
	case ip.IsLoopback() || ip.IsLinkLocalUnicast():
		return 3
	case ip.Is4() && (dockerRange.Contains(ip) || ip.IsPrivate() && ip.As4()[3] == 1):
		return 2
	case ip.IsPrivate():
		return 0
	}
	return 1
}

var dockerRange = netip.MustParsePrefix("172.16.0.0/12")

type wrongServerError struct{}

func (wrongServerError) Error() string { return "connection answers as a different server" }

var errWrongServer = wrongServerError{}

// Set replaces the server list. Used by tests and benchmarks.
func (r *Registry) Set(list ...*Server) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make(map[string]*Server, len(list))
	for _, s := range list {
		next[s.MachineID] = s
	}
	r.servers.Store(&next)
}

// address is the IP of a connection: the one plex.tv reports, or the host of its URI.
func address(c plex.Connection) string {
	if c.Address != "" {
		return c.Address
	}
	u, err := url.Parse(c.URI)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
