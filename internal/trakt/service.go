package trakt

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/index"
	"github.com/jmoruno/plexmor/internal/plex"
	"github.com/jmoruno/plexmor/internal/servers"
)

// Service connects the Trakt client with the accounts, the index and the Plex servers.
type Service struct {
	Client   *Client
	Accounts *accounts.Store
	Store    *index.Store
	Registry *servers.Registry
	Plex     *plex.Client
	Log      *slog.Logger

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at  time.Time
	ids []string
}

// Available reports whether Trakt can be used for an account: configured, enabled, linked and VIP.
func (s *Service) Available(a *accounts.Account) bool {
	t := a.Prefs.Trakt
	return s != nil && s.Client.Configured() && t.Enabled && t.Linked() && t.VIP
}

// ErrNotVIP is returned when the Trakt account is not VIP.
var ErrNotVIP = errors.New("la cuenta de Trakt no es VIP")

// Link finishes the device login: stores the tokens and checks the VIP status.
func (s *Service) Link(ctx context.Context, accountID string, tok Token) (*accounts.Account, error) {
	user, vip, err := s.Client.Settings(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}
	a, err := s.Accounts.Update(accountID, func(a *accounts.Account) {
		a.Prefs.Trakt = accounts.Trakt{
			Enabled: true, Username: user, VIP: vip, SyncWatched: a.Prefs.Trakt.SyncWatched,
			AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, ExpiresAt: tok.ExpiresAt(),
			LastSync: time.Now().Add(-30 * 24 * time.Hour).Unix(),
		}
	})
	if err == nil && !vip {
		err = ErrNotVIP
	}
	return a, err
}

// token returns a valid access token, refreshing it when it is about to expire.
func (s *Service) token(ctx context.Context, a *accounts.Account) (string, error) {
	t := a.Prefs.Trakt
	if t.ExpiresAt == 0 || time.Until(time.Unix(t.ExpiresAt, 0)) > 24*time.Hour {
		return t.AccessToken, nil
	}
	nt, err := s.Client.Refresh(ctx, t.RefreshToken)
	if err != nil {
		return t.AccessToken, err
	}
	_, err = s.Accounts.Update(a.ID, func(a *accounts.Account) {
		a.Prefs.Trakt.AccessToken, a.Prefs.Trakt.RefreshToken, a.Prefs.Trakt.ExpiresAt = nt.AccessToken, nt.RefreshToken, nt.ExpiresAt()
	})
	return nt.AccessToken, err
}

// List returns a Trakt list (watchlist or recommended) as IMDb ids, cached for ten minutes.
func (s *Service) List(ctx context.Context, a *accounts.Account, kind, list string) ([]string, error) {
	key := a.ID + "/" + kind + "/" + list
	s.mu.Lock()
	if c, ok := s.cache[key]; ok && time.Since(c.at) < 10*time.Minute {
		s.mu.Unlock()
		return c.ids, nil
	}
	s.mu.Unlock()
	access, err := s.token(ctx, a)
	if err != nil {
		return nil, err
	}
	var ids []string
	switch list {
	case "watchlist":
		ids, err = s.Client.Watchlist(ctx, access, kind)
	case "recommended":
		ids, err = s.Client.Recommended(ctx, access, kind)
	default:
		return nil, errors.New("unknown list")
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cached{}
	}
	s.cache[key] = cached{time.Now(), ids}
	s.mu.Unlock()
	return ids, nil
}

// SyncWatched marks in Plex what each account watched according to Trakt since the last run.
func (s *Service) SyncWatched(ctx context.Context) {
	for _, a := range s.Accounts.All() {
		if !s.Available(a) || !a.Prefs.Trakt.SyncWatched {
			continue
		}
		if n, err := s.syncAccount(ctx, a); err != nil {
			s.Log.Warn("trakt: could not sync the history", "account", a.Username, "error", err)
		} else if n > 0 {
			s.Log.Info("trakt: marked as watched in Plex", "account", a.Username, "items", n)
		}
	}
}

func (s *Service) syncAccount(ctx context.Context, a *accounts.Account) (int, error) {
	access, err := s.token(ctx, a)
	if err != nil {
		return 0, err
	}
	since := time.Unix(a.Prefs.Trakt.LastSync, 0)
	started := time.Now()
	history, err := s.Client.History(ctx, access, since)
	if err != nil {
		return 0, err
	}
	marked := 0
	for _, w := range history {
		key := "imdb:" + w.IMDb
		if w.Type == "episode" {
			key = index.EpisodeKey(key, w.Season, w.Episode)
		}
		for _, it := range s.Store.Lookup(key) {
			srv := s.Registry.Get(it.Server)
			if srv == nil || !srv.Healthy() || srv.TokenFor(a.ID) == "" {
				continue
			}
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := s.Plex.Scrobble(c, plex.Server{URI: srv.Best.URI, Token: srv.TokenFor(a.ID)}, it.RatingKey)
			cancel()
			if err != nil {
				s.Log.Debug("trakt: scrobble failed", "server", srv.Label(), "item", it.Title, "error", err)
				continue
			}
			marked++
		}
	}
	_, err = s.Accounts.Update(a.ID, func(a *accounts.Account) { a.Prefs.Trakt.LastSync = started.Unix() })
	return marked, err
}
