// Command plexmor is a Stremio addon that serves your Plex servers (own and shared) from a local index.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/config"
	"github.com/jmoruno/plexmor/internal/httpsec"
	"github.com/jmoruno/plexmor/internal/index"
	"github.com/jmoruno/plexmor/internal/metrics"
	"github.com/jmoruno/plexmor/internal/panel"
	"github.com/jmoruno/plexmor/internal/plex"
	"github.com/jmoruno/plexmor/internal/servers"
	"github.com/jmoruno/plexmor/internal/stremio"
	"github.com/jmoruno/plexmor/internal/trakt"
)

// version is set at build time with -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	// "plexmor health" is the container healthcheck: the image has no shell or curl.
	if len(os.Args) > 1 && os.Args[1] == "health" {
		if err := health(os.Getenv("LISTEN")); err != nil {
			slog.Error("unhealthy", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("plexmor stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	languages := make([]string, 0, len(cfg.Languages))
	for _, l := range cfg.Languages {
		languages = append(languages, index.NormalizeLanguage(l))
	}

	clientID, idErr := clientIdentifier(cfg.DataDir)
	if idErr != nil {
		// Without a writable data folder Plexmor still works; it just reindexes on every start.
		log.Warn("data folder is not writable: the index and the accounts will not survive restarts", "dir", cfg.DataDir, "error", idErr)
	}
	client := plex.NewClient(clientID, version)

	secret, err := accounts.Secret(cfg.Secret, cfg.DataDir)
	if err != nil {
		log.Warn("could not save the secret: sessions and accounts will not survive restarts", "error", err)
	}
	accs, err := accounts.Open(filepath.Join(cfg.DataDir, "accounts.json"), secret)
	if err != nil {
		return err
	}
	adminKey := cfg.Key
	if adminKey == "" {
		// The address of the first versions is kept, so installed addons keep working.
		if adminKey, err = addonKey(cfg.DataDir); err != nil {
			log.Warn("could not save the addon key", "error", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bootstrapAdmin(ctx, cfg, client, accs, adminKey, log)

	store := index.NewStore()
	snapshotPath := filepath.Join(cfg.DataDir, "index.gob")
	if err := store.Load(snapshotPath); err != nil {
		log.Warn("ignoring unreadable index snapshot", "error", err)
	} else if n := store.Count(); n > 0 {
		log.Info("index loaded from disk", "items", n)
	}

	credentials := func() []servers.Credential {
		var out []servers.Credential
		for _, a := range accs.All() {
			if a.PlexToken != "" {
				out = append(out, servers.Credential{AccountID: a.ID, Token: a.PlexToken})
			}
		}
		return out
	}
	registry := servers.NewRegistry(client, cfg.PlexTVURL, credentials, cfg.Servers, 6*time.Second, log)
	indexer := &index.Indexer{
		Client: client, Store: store, Log: log,
		Timeout: cfg.PlexTimeout, PageSize: 500, BatchSize: 40, Concurrency: 4,
		MaxConsecutiveFailures: 3, RetryDelay: time.Second,
	}

	m := metrics.New()
	m.Gauge("plexmor_index_items", "Movies and episodes in the index.", func() []metrics.Sample {
		return []metrics.Sample{{Value: float64(store.Count())}}
	})
	m.Gauge("plexmor_accounts", "Plexmor users.", func() []metrics.Sample {
		return []metrics.Sample{{Value: float64(len(accs.All()))}}
	})
	m.Gauge("plexmor_server_up", "1 when the server answers.", func() []metrics.Sample {
		var out []metrics.Sample
		for _, s := range registry.All() {
			v := 0.0
			if s.Healthy() {
				v = 1
			}
			out = append(out, metrics.Sample{Labels: map[string]string{"server": s.Label()}, Value: v})
		}
		return out
	})
	m.Gauge("plexmor_server_latency_seconds", "Latency of the best connection.", func() []metrics.Sample {
		var out []metrics.Sample
		for _, s := range registry.All() {
			if s.Best != nil {
				out = append(out, metrics.Sample{Labels: map[string]string{"server": s.Label()}, Value: s.Best.Latency.Seconds()})
			}
		}
		return out
	})

	traktSvc := &trakt.Service{
		Client:   &trakt.Client{ClientID: cfg.TraktClientID, ClientSecret: cfg.TraktClientSecret},
		Accounts: accs, Store: store, Registry: registry, Plex: client, Log: log,
	}
	refreshNow := make(chan struct{}, 1)
	reindexNow := make(chan struct{}, 1)

	handler := &stremio.Handler{
		Store: store, Registry: registry, Accounts: accs, Client: client, Discover: cfg.DiscoverURL,
		PublicURL: cfg.PublicURL, Version: version, Log: log, Metrics: m, Trakt: traktSvc,
		Languages: languages, MaxStreams: cfg.MaxStreams, Language: cfg.Language,
	}
	web := &panel.Panel{
		Accounts: accs, Registry: registry, Store: store, Client: client, PlexTV: cfg.PlexTVURL,
		Trakt: traktSvc, Version: version, PublicURL: cfg.PublicURL, Languages: languages, Language: cfg.Language, Log: log,
		OnAccountsChanged: func() { signal1(refreshNow) },
		Reindex:           func() { signal1(reindexNow) },
	}
	mux := http.NewServeMux()
	web.Routes(mux)
	handler.Routes(mux)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{"ok":true}` + "\n"))
	})
	// Metrics name the servers and users count: only for Prometheus (METRICS_TOKEN) and administrators.
	mux.Handle("GET /metrics", httpsec.Protected(m, cfg.MetricsToken, web.IsAdmin))

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           httpsec.Headers(stremio.CORS(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    64 << 10,
		WriteTimeout:      60 * time.Second, // the video proxy lifts it for its own responses
		IdleTimeout:       120 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		// The addon URLs are secrets: they are shown in the panel, never in the logs.
		log.Info("plexmor listening", "version", version, "addr", cfg.Listen, "accounts", len(accs.All()),
			"language", cfg.Language, "languages", strings.Join(languages, ","), "trakt", traktSvc.Client.Configured(),
			"metrics_token", cfg.MetricsToken != "")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	ready := make(chan struct{}) // closed once the server list has been read from plex.tv
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		watchServers(ctx, cfg, registry, refreshNow, reindexNow, ready, log)
	}()
	go func() {
		defer wg.Done()
		indexLoop(ctx, cfg, registry, indexer, store, snapshotPath, reindexNow, ready, log)
	}()
	go func() {
		defer wg.Done()
		maintainAccounts(ctx, cfg, client, accs, traktSvc, log)
	}()

	err = nil
	select {
	case err = <-serveErr:
		stop()
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	wg.Wait()
	if saveErr := store.Save(snapshotPath); saveErr != nil {
		log.Warn("could not save the index", "error", saveErr)
	}
	return err
}

// health asks the running Plexmor (listening on listen, ":7254" by default) whether it is up.
func health(listen string) error {
	if listen == "" {
		listen = ":7254"
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// signal1 wakes a loop without blocking (a pending signal is enough).
func signal1(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// bootstrapAdmin turns the Plex token of the configuration (PLEX_TOKEN or Plex's Preferences.xml)
// into the administrator account, keeping the addon address of earlier versions.
func bootstrapAdmin(ctx context.Context, cfg config.Config, client *plex.Client, accs *accounts.Store, key string, log *slog.Logger) {
	if cfg.PlexToken == "" {
		if len(accs.All()) == 0 {
			log.Info("no Plex token configured: the first person to sign in to the panel becomes the administrator")
		}
		return
	}
	c, cancel := context.WithTimeout(ctx, cfg.PlexTimeout)
	user, userErr := client.User(c, cfg.PlexTVURL, cfg.PlexToken)
	cancel()
	target := accs.Get(key)
	if target == nil && userErr == nil {
		target = accs.ByUUID(user.UUID)
	}
	if target != nil {
		_, err := accs.Update(target.ID, func(a *accounts.Account) {
			a.PlexToken, a.Admin = cfg.PlexToken, true
			if userErr == nil {
				a.PlexUUID, a.Username, a.Thumb = user.UUID, user.Name(), user.Thumb
			}
		})
		if err != nil {
			log.Warn("could not update the administrator", "error", err)
		}
		return
	}
	a := &accounts.Account{ID: key, PlexToken: cfg.PlexToken, Admin: true, Created: time.Now().Unix(), Username: "admin"}
	if userErr == nil {
		a.PlexUUID, a.Username, a.Thumb = user.UUID, user.Name(), user.Thumb
	} else {
		log.Warn("plex.tv did not answer: the administrator's Plex account will be identified later", "error", userErr)
	}
	if err := accs.Put(a); err != nil {
		log.Warn("could not save the administrator", "error", err)
	}
}

// maintainAccounts completes accounts created while plex.tv was down and copies Trakt history to Plex.
func maintainAccounts(ctx context.Context, cfg config.Config, client *plex.Client, accs *accounts.Store, tr *trakt.Service, log *slog.Logger) {
	identify := time.NewTicker(time.Minute)
	defer identify.Stop()
	traktTick := time.NewTicker(cfg.TraktSyncInterval)
	defer traktTick.Stop()
	if tr.Client.Configured() {
		go tr.SyncWatched(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-identify.C:
			for _, a := range accs.All() {
				if a.PlexUUID != "" || a.PlexToken == "" {
					continue
				}
				c, cancel := context.WithTimeout(ctx, cfg.PlexTimeout)
				user, err := client.User(c, cfg.PlexTVURL, a.PlexToken)
				cancel()
				if err != nil {
					continue
				}
				_, _ = accs.Update(a.ID, func(a *accounts.Account) { a.PlexUUID, a.Username, a.Thumb = user.UUID, user.Name(), user.Thumb })
				log.Info("account identified", "user", user.Name())
			}
		case <-traktTick.C:
			if tr.Client.Configured() {
				tr.SyncWatched(ctx)
			}
		}
	}
}

// watchServers keeps the server list and the best connection to each server up to date.
// It runs on its own so a long sync never delays noticing that a server went down.
func watchServers(ctx context.Context, cfg config.Config, registry *servers.Registry, refreshNow <-chan struct{}, reindexNow chan<- struct{}, ready chan<- struct{}, log *slog.Logger) {
	refresh := func() bool {
		c, cancel := context.WithTimeout(ctx, cfg.PlexTimeout)
		defer cancel()
		if err := registry.Refresh(c); err != nil {
			if ctx.Err() == nil {
				log.Error("could not read the server list from plex.tv", "error", err)
			}
			return false
		}
		return true
	}
	// Until plex.tv answers once there is nothing to serve: retry with backoff (10 s up to 5 min).
	for delay := 10 * time.Second; !refresh(); delay = min(delay*2, 5*time.Minute) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
	registry.Probe(ctx)
	close(ready)

	resources := time.NewTicker(cfg.ResourcesInterval)
	health := time.NewTicker(cfg.HealthInterval)
	defer resources.Stop()
	defer health.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-resources.C:
			if refresh() {
				registry.Probe(ctx)
			}
		case <-health.C:
			registry.Probe(ctx)
		case <-refreshNow:
			// Someone signed in: discover their servers and index the new ones.
			if refresh() {
				registry.Probe(ctx)
				signal1(reindexNow)
			}
		}
	}
}

// indexLoop indexes every reachable server once the server list is known, then periodically.
func indexLoop(ctx context.Context, cfg config.Config, registry *servers.Registry, indexer *index.Indexer, store *index.Store, snapshotPath string, reindexNow <-chan struct{}, ready <-chan struct{}, log *slog.Logger) {
	select {
	case <-ctx.Done():
		return
	case <-ready:
	}
	syncAll := func() {
		syncServers(ctx, registry, indexer, store, log)
		if err := store.Save(snapshotPath); err != nil && ctx.Err() == nil {
			log.Warn("could not save the index", "error", err)
		}
	}
	syncAll()
	tick := time.NewTicker(cfg.SyncInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			syncAll()
		case <-reindexNow:
			syncAll()
		}
	}
}

// syncServers indexes every reachable server, a few at a time, and drops servers no longer shared.
func syncServers(ctx context.Context, registry *servers.Registry, indexer *index.Indexer, store *index.Store, log *slog.Logger) {
	known := map[string]bool{}
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for _, s := range registry.All() {
		known[s.MachineID] = true
		if !s.Healthy() {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(s *servers.Server) {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			st, err := indexer.Sync(ctx, index.Source{
				MachineID: s.MachineID, Name: s.Label(), Owner: s.Owner, Owned: s.Owned, API: s.API(),
			})
			if err != nil {
				if ctx.Err() == nil {
					log.Warn("sync failed, keeping the previous index", "server", s.Label(), "error", err)
				}
				return
			}
			log.Info("server indexed", "server", s.Label(), "items", st.Items, "detailed", st.Detailed,
				"unchanged", st.Reused, "removed", st.Removed, "pending", st.Pending, "failedLibraries", st.FailedSections,
				"elapsed", time.Since(start).Round(time.Millisecond))
		}(s)
	}
	wg.Wait()
	if len(known) == 0 {
		return // plex.tv unreachable at startup: keep whatever the snapshot had
	}
	for _, id := range store.Servers() {
		if !known[id] {
			store.RemoveServer(id)
			log.Info("server no longer shared, removed from the index", "machineID", id)
		}
	}
}

// clientIdentifier returns a stable identifier for this installation, as Plex expects.
func clientIdentifier(dataDir string) (string, error) {
	return persistentRandom(dataDir, "client-id", "plexmor-")
}

// addonKey returns the addon address of the administrator, created on first start when PLEXMOR_KEY is not set.
func addonKey(dataDir string) (string, error) {
	return persistentRandom(dataDir, "addon-key", "")
}

// persistentRandom reads a random value from dataDir/name, creating it the first time.
func persistentRandom(dataDir, name, prefix string) (string, error) {
	path := filepath.Join(dataDir, name)
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b)), nil
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf) // never fails on supported platforms
	v := prefix + hex.EncodeToString(buf)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return v, err
	}
	return v, os.WriteFile(path, []byte(v+"\n"), 0o600)
}
