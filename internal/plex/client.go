package plex

import (
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

// Product is how Plexmor identifies itself to Plex.
const Product = "Plexmor"

// Client performs requests against plex.tv and Plex Media Servers.
type Client struct {
	HTTP             *http.Client
	ClientIdentifier string
	Version          string
}

// NewClient returns a client with sane defaults. Timeouts are applied per request through contexts.
func NewClient(clientID, version string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 8
	transport.ResponseHeaderTimeout = 60 * time.Second
	return &Client{
		HTTP:             &http.Client{Transport: transport},
		ClientIdentifier: clientID,
		Version:          version,
	}
}

// StatusError is returned when Plex answers with a non-2xx status.
type StatusError struct {
	URL    string
	Status int
}

func (e *StatusError) Error() string { return fmt.Sprintf("plex: %s answered %d", e.URL, e.Status) }

// Unauthorized reports whether the error is a 401/403 from Plex.
func Unauthorized(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden)
}

func (c *Client) get(ctx context.Context, rawURL, token string, query url.Values, out any) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if len(query) > 0 {
		q := u.Query()
		for k, vs := range query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	// plex.tv answers XML unless JSON is asked for explicitly (the bug patched in Plexio).
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", Product)
	req.Header.Set("X-Plex-Version", c.Version)
	req.Header.Set("X-Plex-Client-Identifier", c.ClientIdentifier)
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return &StatusError{URL: redact(u), Status: resp.StatusCode}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("plex: decoding %s: %w", redact(u), err)
	}
	return nil
}

// redact strips the query string so tokens never reach the logs.
func redact(u *url.URL) string {
	c := *u
	c.RawQuery = ""
	return c.String()
}

// Resources lists the servers the account can reach, with their tokens and connections.
func (c *Client) Resources(ctx context.Context, plexTV, token string) ([]Resource, error) {
	var all []Resource
	q := url.Values{"includeHttps": {"1"}, "includeRelay": {"1"}, "includeIPv6": {"0"}}
	if err := c.get(ctx, strings.TrimRight(plexTV, "/")+"/api/v2/resources", token, q, &all); err != nil {
		return nil, err
	}
	servers := all[:0]
	for _, r := range all {
		if r.IsServer() {
			servers = append(servers, r)
		}
	}
	return servers, nil
}

// Server is a reachable Plex Media Server: a base URI plus the token to use with it.
type Server struct {
	URI   string
	Token string
}

func (s Server) url(path string) string { return strings.TrimRight(s.URI, "/") + path }

// Identity answers /identity; it is the cheapest call and is used to probe connections.
func (c *Client) Identity(ctx context.Context, s Server) (Identity, error) {
	var mc mediaContainer[Identity]
	err := c.get(ctx, s.url("/identity"), s.Token, nil, &mc)
	return mc.MediaContainer, err
}

// Sections lists the libraries of a server.
func (c *Client) Sections(ctx context.Context, s Server) ([]Section, error) {
	var mc mediaContainer[sectionList]
	err := c.get(ctx, s.url("/library/sections"), s.Token, nil, &mc)
	return mc.MediaContainer.Directory, err
}

// Plex item types for /library/sections/{key}/all?type=N.
const (
	TypeMovie   = 1
	TypeShow    = 2
	TypeEpisode = 4
)

// Page lists one page of the items of a type in a section, with the total size of the listing.
// Listings carry Media and Part but no Stream; use Metadata for the tracks.
func (c *Client) Page(ctx context.Context, s Server, section string, itemType, start, size int) ([]Metadata, int, error) {
	q := url.Values{
		"type":                   {strconv.Itoa(itemType)},
		"includeGuids":           {"1"},
		"X-Plex-Container-Start": {strconv.Itoa(start)},
		"X-Plex-Container-Size":  {strconv.Itoa(size)},
	}
	var mc mediaContainer[metadataList]
	err := c.get(ctx, s.url("/library/sections/"+url.PathEscape(section)+"/all"), s.Token, q, &mc)
	return mc.MediaContainer.Metadata, mc.MediaContainer.TotalSize, err
}

// Metadata fetches full details (with tracks) for several items in one request.
func (c *Client) Metadata(ctx context.Context, s Server, ratingKeys []string) ([]Metadata, error) {
	if len(ratingKeys) == 0 {
		return nil, nil
	}
	var mc mediaContainer[metadataList]
	path := "/library/metadata/" + strings.Join(ratingKeys, ",")
	err := c.get(ctx, s.url(path), s.Token, nil, &mc)
	return mc.MediaContainer.Metadata, err
}

// do sends a request with the Plex headers. Callers close the body.
func (c *Client) do(ctx context.Context, method, rawURL, token string, query url.Values) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if len(query) > 0 {
		q := u.Query()
		for k, vs := range query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", Product)
	req.Header.Set("X-Plex-Version", c.Version)
	req.Header.Set("X-Plex-Client-Identifier", c.ClientIdentifier)
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, &StatusError{URL: redact(u), Status: resp.StatusCode}
	}
	return resp, nil
}

// CreatePin starts a PIN login on plex.tv.
func (c *Client) CreatePin(ctx context.Context, plexTV string) (Pin, error) {
	resp, err := c.do(ctx, http.MethodPost, strings.TrimRight(plexTV, "/")+"/api/v2/pins", "", url.Values{"strong": {"true"}})
	if err != nil {
		return Pin{}, err
	}
	defer resp.Body.Close()
	var p Pin
	return p, json.NewDecoder(resp.Body).Decode(&p)
}

// CheckPin returns the PIN, with AuthToken set once the user has signed in.
func (c *Client) CheckPin(ctx context.Context, plexTV string, id int64) (Pin, error) {
	var p Pin
	err := c.get(ctx, strings.TrimRight(plexTV, "/")+"/api/v2/pins/"+strconv.FormatInt(id, 10), "", nil, &p)
	return p, err
}

// AuthURL is the plex.tv page where the user signs in to approve a PIN.
func (c *Client) AuthURL(code, forwardURL string) string {
	q := url.Values{
		"clientID":                  {c.ClientIdentifier},
		"code":                      {code},
		"context[device][product]":  {Product},
		"context[device][platform]": {"Web"},
		"context[device][device]":   {Product},
		"context[device][version]":  {c.Version},
		"context[device][layout]":   {"desktop"},
	}
	if forwardURL != "" {
		q.Set("forwardUrl", forwardURL)
	}
	return "https://app.plex.tv/auth#?" + q.Encode()
}

// User returns the account behind a token.
func (c *Client) User(ctx context.Context, plexTV, token string) (User, error) {
	var u User
	err := c.get(ctx, strings.TrimRight(plexTV, "/")+"/api/v2/user", token, nil, &u)
	return u, err
}

// OnDeck lists what the user is watching on a server ("Seguir viendo").
func (c *Client) OnDeck(ctx context.Context, s Server) ([]Metadata, error) {
	var mc mediaContainer[metadataList]
	err := c.get(ctx, s.url("/library/onDeck"), s.Token, url.Values{"includeGuids": {"1"}}, &mc)
	return mc.MediaContainer.Metadata, err
}

// Watchlist reads the user's Plex watchlist from the discover service.
func (c *Client) Watchlist(ctx context.Context, discoverURL, token string) ([]Metadata, error) {
	var all []Metadata
	for start := 0; ; {
		q := url.Values{"includeGuids": {"1"}, "X-Plex-Container-Start": {strconv.Itoa(start)}, "X-Plex-Container-Size": {"200"}}
		var mc mediaContainer[metadataList]
		if err := c.get(ctx, strings.TrimRight(discoverURL, "/")+"/library/sections/watchlist/all", token, q, &mc); err != nil {
			return all, err
		}
		all = append(all, mc.MediaContainer.Metadata...)
		start += len(mc.MediaContainer.Metadata)
		if len(mc.MediaContainer.Metadata) == 0 || start >= mc.MediaContainer.TotalSize {
			return all, nil
		}
	}
}

// Scrobble marks an item as watched on a server.
func (c *Client) Scrobble(ctx context.Context, s Server, ratingKey string) error {
	return c.get(ctx, s.url("/:/scrobble"), s.Token, url.Values{"key": {ratingKey}, "identifier": {"com.plexapp.plugins.library"}}, nil)
}
