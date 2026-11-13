// Package metrics exposes counters and latencies in the Prometheus text format, without
// dependencies.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Registry holds request counters, latency histograms and gauges read on demand.
type Registry struct {
	mu       sync.Mutex
	requests map[string]*route
	gauges   []gauge
}

type route struct {
	count   map[int]uint64 // by status class (2, 4, 5)
	buckets []uint64
	sum     float64
	n       uint64
}

type gauge struct {
	name, help string
	fn         func() []Sample
}

// Sample is one gauge value with optional labels ("server" -> "Casa").
type Sample struct {
	Labels map[string]string
	Value  float64
}

var bounds = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

// New returns an empty registry.
func New() *Registry { return &Registry{requests: map[string]*route{}} }

// Observe records one request of a route.
func (r *Registry) Observe(name string, status int, d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rt := r.requests[name]
	if rt == nil {
		rt = &route{count: map[int]uint64{}, buckets: make([]uint64, len(bounds))}
		r.requests[name] = rt
	}
	rt.count[status/100]++
	sec := d.Seconds()
	for i, b := range bounds {
		if sec <= b {
			rt.buckets[i]++
		}
	}
	rt.sum += sec
	rt.n++
}

// Gauge registers a value computed when /metrics is read.
func (r *Registry) Gauge(name, help string, fn func() []Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges = append(r.gauges, gauge{name, help, fn})
}

// Wrap measures a handler under a route name.
func (r *Registry) Wrap(name string, h http.HandlerFunc) http.HandlerFunc {
	if r == nil {
		return h
	}
	return func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h(sw, req)
		r.Observe(name, sw.status, time.Since(start))
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the original writer (deadlines, flushing).
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// ServeHTTP writes every metric in the Prometheus text format.
func (r *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	r.write(w)
}

func (r *Registry) write(w io.Writer) {
	r.mu.Lock()
	names := make([]string, 0, len(r.requests))
	for n := range r.requests {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintln(w, "# HELP plexmor_requests_total Requests served, by route and status class.")
	fmt.Fprintln(w, "# TYPE plexmor_requests_total counter")
	for _, n := range names {
		for _, class := range []int{2, 3, 4, 5} {
			if c := r.requests[n].count[class]; c > 0 {
				fmt.Fprintf(w, "plexmor_requests_total{route=%q,code=\"%dxx\"} %d\n", n, class, c)
			}
		}
	}
	fmt.Fprintln(w, "# HELP plexmor_request_duration_seconds Time to answer, by route.")
	fmt.Fprintln(w, "# TYPE plexmor_request_duration_seconds histogram")
	for _, n := range names {
		rt := r.requests[n]
		for i, b := range bounds {
			fmt.Fprintf(w, "plexmor_request_duration_seconds_bucket{route=%q,le=\"%g\"} %d\n", n, b, rt.buckets[i])
		}
		fmt.Fprintf(w, "plexmor_request_duration_seconds_bucket{route=%q,le=\"+Inf\"} %d\n", n, rt.n)
		fmt.Fprintf(w, "plexmor_request_duration_seconds_sum{route=%q} %g\n", n, rt.sum)
		fmt.Fprintf(w, "plexmor_request_duration_seconds_count{route=%q} %d\n", n, rt.n)
	}
	gauges := append([]gauge(nil), r.gauges...)
	r.mu.Unlock()
	for _, g := range gauges {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", g.name, g.help, g.name)
		for _, s := range g.fn() {
			fmt.Fprintf(w, "%s%s %g\n", g.name, labels(s.Labels), s.Value)
		}
	}
}

func labels(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, m[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
