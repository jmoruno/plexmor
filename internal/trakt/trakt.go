// Package trakt is the optional Trakt module: lists filtered to what the Plex servers have,
// and marking in Plex what Trakt says was watched. It only works for Trakt VIP accounts.
package trakt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAPI is the Trakt API.
const DefaultAPI = "https://api.trakt.tv"

// Client talks to the Trakt API with an application's credentials.
type Client struct {
	API          string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
}

// Configured reports whether the Trakt application credentials are set.
func (c *Client) Configured() bool { return c != nil && c.ClientID != "" && c.ClientSecret != "" }

// ErrPending means the user has not approved the device code yet.
var ErrPending = errors.New("trakt: authorization pending")

// DeviceCode is the code the user types at the verification URL.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// Token is an OAuth token.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	CreatedAt    int64  `json:"created_at"`
}

// ExpiresAt is the unix time the access token expires.
func (t Token) ExpiresAt() int64 { return t.CreatedAt + t.ExpiresIn }

// StatusError is a non-2xx answer.
type StatusError struct {
	Path   string
	Status int
}

func (e *StatusError) Error() string { return fmt.Sprintf("trakt: %s answered %d", e.Path, e.Status) }

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) base() string {
	if c.API != "" {
		return strings.TrimRight(c.API, "/")
	}
	return DefaultAPI
}

func (c *Client) call(ctx context.Context, method, path, access string, body, out any) (http.Header, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", c.ClientID)
	req.Header.Set("User-Agent", "Plexmor")
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		p, _, _ := strings.Cut(path, "?")
		return resp.Header, &StatusError{Path: p, Status: resp.StatusCode}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.Header, fmt.Errorf("trakt: decoding %s: %w", path, err)
		}
	}
	return resp.Header, nil
}

// StartDevice begins the device login.
func (c *Client) StartDevice(ctx context.Context) (DeviceCode, error) {
	var dc DeviceCode
	_, err := c.call(ctx, http.MethodPost, "/oauth/device/code", "", map[string]string{"client_id": c.ClientID}, &dc)
	return dc, err
}

// PollDevice returns the token once the user approved the code, or ErrPending.
func (c *Client) PollDevice(ctx context.Context, deviceCode string) (Token, error) {
	var t Token
	_, err := c.call(ctx, http.MethodPost, "/oauth/device/token", "", map[string]string{
		"code": deviceCode, "client_id": c.ClientID, "client_secret": c.ClientSecret,
	}, &t)
	var se *StatusError
	if errors.As(err, &se) && (se.Status == http.StatusBadRequest || se.Status == http.StatusTooManyRequests) {
		return t, ErrPending
	}
	return t, err
}

// Refresh renews an access token.
func (c *Client) Refresh(ctx context.Context, refresh string) (Token, error) {
	var t Token
	_, err := c.call(ctx, http.MethodPost, "/oauth/token", "", map[string]string{
		"refresh_token": refresh, "client_id": c.ClientID, "client_secret": c.ClientSecret,
		"redirect_uri": "urn:ietf:wg:oauth:2.0:oob", "grant_type": "refresh_token",
	}, &t)
	return t, err
}

// Settings returns the username and whether the account is VIP.
func (c *Client) Settings(ctx context.Context, access string) (username string, vip bool, err error) {
	var s struct {
		User struct {
			Username string `json:"username"`
			VIP      bool   `json:"vip"`
			VIPEP    bool   `json:"vip_ep"`
		} `json:"user"`
	}
	_, err = c.call(ctx, http.MethodGet, "/users/settings", access, nil, &s)
	return s.User.Username, s.User.VIP || s.User.VIPEP, err
}

type ids struct {
	IMDb string `json:"imdb"`
}

type media struct {
	IDs ids `json:"ids"`
}

func traktKind(kind string) string {
	if kind == "series" {
		return "shows"
	}
	return "movies"
}

// Watchlist returns the IMDb ids of the user's watchlist, newest first.
func (c *Client) Watchlist(ctx context.Context, access, kind string) ([]string, error) {
	var list []struct {
		Movie *media `json:"movie"`
		Show  *media `json:"show"`
	}
	_, err := c.call(ctx, http.MethodGet, "/sync/watchlist/"+traktKind(kind)+"/added", access, nil, &list)
	var out []string
	for _, e := range list {
		m := e.Movie
		if m == nil {
			m = e.Show
		}
		if m != nil && m.IDs.IMDb != "" {
			out = append(out, m.IDs.IMDb)
		}
	}
	return out, err
}

// Recommended returns the IMDb ids Trakt recommends to the user.
func (c *Client) Recommended(ctx context.Context, access, kind string) ([]string, error) {
	var list []media
	_, err := c.call(ctx, http.MethodGet, "/recommendations/"+traktKind(kind)+"?limit=100&ignore_collected=false", access, nil, &list)
	var out []string
	for _, m := range list {
		if m.IDs.IMDb != "" {
			out = append(out, m.IDs.IMDb)
		}
	}
	return out, err
}

// Watched is an entry of the history.
type Watched struct {
	Type      string // movie or episode
	IMDb      string // movie, or series of the episode
	Season    int
	Episode   int
	WatchedAt time.Time
}

// History returns what was watched since a time, oldest first.
func (c *Client) History(ctx context.Context, access string, since time.Time) ([]Watched, error) {
	var out []Watched
	for page := 1; page <= 50; page++ {
		q := url.Values{"start_at": {since.UTC().Format(time.RFC3339)}, "limit": {"100"}, "page": {strconv.Itoa(page)}}
		var list []struct {
			Type      string    `json:"type"`
			WatchedAt time.Time `json:"watched_at"`
			Movie     *media    `json:"movie"`
			Show      *media    `json:"show"`
			Episode   *struct {
				Season int `json:"season"`
				Number int `json:"number"`
			} `json:"episode"`
		}
		hdr, err := c.call(ctx, http.MethodGet, "/sync/history?"+q.Encode(), access, nil, &list)
		if err != nil {
			return out, err
		}
		for _, e := range list {
			w := Watched{Type: e.Type, WatchedAt: e.WatchedAt}
			switch {
			case e.Type == "movie" && e.Movie != nil:
				w.IMDb = e.Movie.IDs.IMDb
			case e.Type == "episode" && e.Show != nil && e.Episode != nil:
				w.IMDb, w.Season, w.Episode = e.Show.IDs.IMDb, e.Episode.Season, e.Episode.Number
			}
			if w.IMDb != "" {
				out = append(out, w)
			}
		}
		pages, _ := strconv.Atoi(hdr.Get("X-Pagination-Page-Count"))
		if page >= pages {
			break
		}
	}
	// Trakt answers newest first; marking in order keeps "last watched" right in Plex.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
