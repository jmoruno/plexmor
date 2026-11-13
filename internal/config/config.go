// Package config loads Plexmor's settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jmoruno/plexmor/internal/i18n"
)

// Config holds every runtime setting. All values come from the environment so the
// stack file in Portainer is the single source of truth.
type Config struct {
	// PlexToken is the account token used to discover the servers (own and shared).
	// When PLEX_TOKEN is empty it is read from the Plex server's Preferences.xml (PLEX_PREFERENCES).
	PlexToken string
	// PlexTVURL is the base URL of the plex.tv API (overridable for tests).
	PlexTVURL string
	// Listen is the HTTP address, e.g. ":7254".
	Listen string
	// DataDir holds the index snapshot and the client identifier.
	DataDir string
	// Key is the addon URL of the administrator (/{key}/manifest.json); created on first start when empty.
	Key string
	// PublicURL is how clients reach Plexmor (https://plexmor.example.com); taken from each request when empty.
	PublicURL string
	// Secret encrypts the tokens and signs the panel sessions; a random one is kept in DataDir when empty.
	Secret string
	// DiscoverURL is Plex's discover service (watchlist).
	DiscoverURL string
	// TraktClientID and TraktClientSecret enable the Trakt module (a Trakt API application).
	TraktClientID     string
	TraktClientSecret string
	// TraktSyncInterval is how often watched history is copied from Trakt to Plex.
	TraktSyncInterval time.Duration
	// Language is the default interface language (es or en) for the panel and the texts in Stremio;
	// each user can change it in the panel.
	Language string
	// Languages is the default audio language preference, best first (castellano, latino, ingles...).
	// When LANGUAGES is empty it follows Language.
	Languages []string
	// MetricsToken lets Prometheus read /metrics with "Authorization: Bearer <token>". Without it
	// only administrators signed in to the panel can see the metrics.
	MetricsToken string
	// Servers limits the servers used (by name, case-insensitive); empty means all.
	Servers []string
	// MaxStreams caps the streams returned per title (0 = no limit).
	MaxStreams int
	// SyncInterval is how often libraries are checked for changes.
	SyncInterval time.Duration
	// HealthInterval is how often server connections are probed.
	HealthInterval time.Duration
	// ResourcesInterval is how often the server list is refreshed from plex.tv.
	ResourcesInterval time.Duration
	// PlexTimeout bounds every request to a Plex server.
	PlexTimeout time.Duration
	// LogLevel is debug, info, warn or error.
	LogLevel slog.Level
}

// Load reads the configuration from the environment.
func Load() (Config, error) {
	c := Config{
		PlexToken:         strings.TrimSpace(os.Getenv("PLEX_TOKEN")),
		PlexTVURL:         env("PLEX_TV_URL", "https://clients.plex.tv"),
		Listen:            env("LISTEN", ":7254"),
		DataDir:           env("DATA_DIR", "/data"),
		Key:               strings.Trim(os.Getenv("PLEXMOR_KEY"), "/ "),
		PublicURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/"),
		Secret:            strings.TrimSpace(os.Getenv("PLEXMOR_SECRET")),
		DiscoverURL:       env("PLEX_DISCOVER_URL", "https://discover.provider.plex.tv"),
		TraktClientID:     strings.TrimSpace(os.Getenv("TRAKT_CLIENT_ID")),
		TraktClientSecret: strings.TrimSpace(os.Getenv("TRAKT_CLIENT_SECRET")),
		TraktSyncInterval: 30 * time.Minute,
		Language:          i18n.Or(os.Getenv("LANGUAGE"), i18n.ES),
		MetricsToken:      strings.TrimSpace(os.Getenv("METRICS_TOKEN")),
		Servers:           list(os.Getenv("SERVERS")),
		SyncInterval:      10 * time.Minute,
		HealthInterval:    time.Minute,
		ResourcesInterval: 30 * time.Minute,
		PlexTimeout:       20 * time.Second,
		LogLevel:          slog.LevelInfo,
	}
	defaultLanguages := "castellano,latino,ingles"
	if c.Language == i18n.EN {
		defaultLanguages = "ingles"
	}
	c.Languages = list(env("LANGUAGES", defaultLanguages))
	var errs []error
	if c.PlexToken == "" {
		// Optional: without a token the first person to sign in to the panel becomes the administrator.
		path := env("PLEX_PREFERENCES", DefaultPreferences)
		token, err := TokenFromPreferences(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("the Plex token could not be read from %s: %w", path, err))
		}
		c.PlexToken = token
	}
	var err error
	if c.MaxStreams, err = intEnv("MAX_STREAMS", 0); err != nil {
		errs = append(errs, err)
	}
	for name, target := range map[string]*time.Duration{
		"SYNC_INTERVAL":       &c.SyncInterval,
		"HEALTH_INTERVAL":     &c.HealthInterval,
		"RESOURCES_INTERVAL":  &c.ResourcesInterval,
		"PLEX_TIMEOUT":        &c.PlexTimeout,
		"TRAKT_SYNC_INTERVAL": &c.TraktSyncInterval,
	} {
		if err := durationEnv(name, target); err != nil {
			errs = append(errs, err)
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
		}
	}
	return c, errors.Join(errs...)
}

// DefaultPreferences is where the compose file mounts the Plex server's configuration folder.
const DefaultPreferences = "/plex/Preferences.xml"

var onlineToken = regexp.MustCompile(`\bPlexOnlineToken="([^"]+)"`)

// TokenFromPreferences reads the account token of the server owner from Plex's Preferences.xml,
// so the token never has to be copied by hand.
func TokenFromPreferences(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m := onlineToken.FindSubmatch(data)
	if m == nil {
		return "", errors.New("PlexOnlineToken not found (is the server signed in to a Plex account?)")
	}
	return string(m[1]), nil
}

func env(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func list(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.ToLower(strings.TrimSpace(part)); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func intEnv(name string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def, fmt.Errorf("%s: must be a non-negative integer", name)
	}
	return n, nil
}

func durationEnv(name string, target *time.Duration) error {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fmt.Errorf("%s: must be a positive duration such as 10m", name)
	}
	*target = d
	return nil
}
