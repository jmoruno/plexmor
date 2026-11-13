// Package accounts keeps the Plexmor users: who they are on Plex, their token (encrypted at
// rest), their preferences and their personal addon URL.
package accounts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Stream modes.
const (
	ModeAuto   = "auto"   // through Plexmor only when the server is reachable only from home
	ModeProxy  = "proxy"  // always through Plexmor: the Plex token never leaves home
	ModeDirect = "direct" // always straight to Plex
)

// Prefs are the settings of a user.
type Prefs struct {
	Languages        []string // audio preference, best first
	MaxStreams       int      // 0 = no limit
	Resolutions      []string // allowed: 4k, 1440, 1080, 720, sd; empty = all
	ExcludedServers  []string // machine identifiers
	ExcludedSections []string // "machineID/sectionKey"
	StreamMode       string
	Transcode        bool // also offer 1080p and 720p versions transcoded by Plex
	HideCatalogs     bool
	UILanguage       string // es or en for the panel and the texts in Stremio; empty = server default
	Trakt            Trakt
}

// Trakt is the optional Trakt link of a user.
type Trakt struct {
	Enabled      bool
	Username     string
	VIP          bool
	AccessToken  string `json:"-"`
	RefreshToken string `json:"-"`
	ExpiresAt    int64
	SyncWatched  bool  // mark in Plex what Trakt says was watched
	LastSync     int64 // unix seconds of the last history read
}

// Linked reports whether the user connected a Trakt account.
func (t Trakt) Linked() bool { return t.AccessToken != "" }

// Account is a Plexmor user. Values are immutable once stored: use With* or copy and Put.
type Account struct {
	ID        string // opaque, used in the addon URL
	PlexUUID  string
	Username  string
	Thumb     string
	PlexToken string `json:"-"`
	Admin     bool
	Created   int64
	LastSeen  int64
	Prefs     Prefs
}

// ServerAllowed reports whether the user wants a server.
func (a *Account) ServerAllowed(machineID string) bool {
	for _, s := range a.Prefs.ExcludedServers {
		if s == machineID {
			return false
		}
	}
	return true
}

// SectionAllowed reports whether the user wants a library.
func (a *Account) SectionAllowed(machineID, section string) bool {
	if !a.ServerAllowed(machineID) {
		return false
	}
	key := machineID + "/" + section
	for _, s := range a.Prefs.ExcludedSections {
		if s == key {
			return false
		}
	}
	return true
}

// Store keeps the accounts in memory and in a JSON file with encrypted tokens.
type Store struct {
	path string
	aead cipher.AEAD
	mac  []byte

	mu   sync.Mutex
	byID atomic.Pointer[map[string]*Account]
}

// Open loads the accounts file (missing is fine). secret encrypts the tokens and signs sessions.
func Open(path string, secret []byte) (*Store, error) {
	key := sha256.Sum256(append([]byte("plexmor-tokens:"), secret...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	mac := sha256.Sum256(append([]byte("plexmor-sessions:"), secret...))
	s := &Store{path: path, aead: aead, mac: mac[:]}
	empty := map[string]*Account{}
	s.byID.Store(&empty)
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Secret returns the value of PLEXMOR_SECRET or a random secret saved in dataDir/secret.
func Secret(env, dataDir string) ([]byte, error) {
	if env != "" {
		return []byte(env), nil
	}
	path := filepath.Join(dataDir, "secret")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
		return []byte(strings.TrimSpace(string(b))), nil
	}
	v := RandomID(32)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return []byte(v), err
	}
	return []byte(v), os.WriteFile(path, []byte(v+"\n"), 0o600)
}

// RandomID returns n random bytes as hex.
func RandomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Get returns an account by id, or nil.
func (s *Store) Get(id string) *Account { return (*s.byID.Load())[id] }

// ByUUID returns the account of a Plex user, or nil.
func (s *Store) ByUUID(uuid string) *Account {
	for _, a := range *s.byID.Load() {
		if a.PlexUUID == uuid {
			return a
		}
	}
	return nil
}

// All returns the accounts, admins first, then by name.
func (s *Store) All() []*Account {
	m := *s.byID.Load()
	out := make([]*Account, 0, len(m))
	for _, a := range m {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Admin != out[j].Admin {
			return out[i].Admin
		}
		return strings.ToLower(out[i].Username) < strings.ToLower(out[j].Username)
	})
	return out
}

// Put stores (or replaces) an account and saves the file.
func (s *Store) Put(a *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := *s.byID.Load()
	next := make(map[string]*Account, len(cur)+1)
	for k, v := range cur {
		next[k] = v
	}
	next[a.ID] = a
	s.byID.Store(&next)
	return s.save(next)
}

// Update applies fn to a copy of an account and stores it.
func (s *Store) Update(id string, fn func(*Account)) (*Account, error) {
	cur := s.Get(id)
	if cur == nil {
		return nil, errors.New("account not found")
	}
	cp := *cur
	cp.Prefs.Languages = append([]string(nil), cur.Prefs.Languages...)
	cp.Prefs.Resolutions = append([]string(nil), cur.Prefs.Resolutions...)
	cp.Prefs.ExcludedServers = append([]string(nil), cur.Prefs.ExcludedServers...)
	cp.Prefs.ExcludedSections = append([]string(nil), cur.Prefs.ExcludedSections...)
	fn(&cp)
	return &cp, s.Put(&cp)
}

// Delete removes an account.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := *s.byID.Load()
	next := make(map[string]*Account, len(cur))
	for k, v := range cur {
		if k != id {
			next[k] = v
		}
	}
	s.byID.Store(&next)
	return s.save(next)
}

// Touch records that an account was used, at most once a minute (no file write).
func (s *Store) Touch(id string) {
	a := s.Get(id)
	now := time.Now().Unix()
	if a == nil || now-a.LastSeen < 60 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := *s.byID.Load()
	next := make(map[string]*Account, len(cur))
	for k, v := range cur {
		next[k] = v
	}
	cp := *a
	cp.LastSeen = now
	next[id] = &cp
	s.byID.Store(&next)
}

// file is the on-disk format: tokens are sealed with AES-GCM.
type file struct {
	Version  int
	Accounts []fileAccount
}

type fileAccount struct {
	Account
	SealedPlexToken    string `json:",omitempty"`
	SealedTraktAccess  string `json:",omitempty"`
	SealedTraktRefresh string `json:",omitempty"`
}

func (s *Store) save(m map[string]*Account) error {
	f := file{Version: 1}
	for _, a := range m {
		f.Accounts = append(f.Accounts, fileAccount{
			Account:            *a,
			SealedPlexToken:    s.seal(a.PlexToken),
			SealedTraktAccess:  s.seal(a.Prefs.Trakt.AccessToken),
			SealedTraktRefresh: s.seal(a.Prefs.Trakt.RefreshToken),
		})
	}
	sort.Slice(f.Accounts, func(i, j int) bool { return f.Accounts[i].ID < f.Accounts[j].ID })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	m := map[string]*Account{}
	for _, fa := range f.Accounts {
		a := fa.Account
		var err error
		if a.PlexToken, err = s.open(fa.SealedPlexToken); err != nil {
			return fmt.Errorf("accounts: the tokens cannot be decrypted (did PLEXMOR_SECRET change?): %w", err)
		}
		a.Prefs.Trakt.AccessToken, _ = s.open(fa.SealedTraktAccess)
		a.Prefs.Trakt.RefreshToken, _ = s.open(fa.SealedTraktRefresh)
		m[a.ID] = &a
	}
	s.byID.Store(&m)
	return nil
}

func (s *Store) seal(plain string) string {
	if plain == "" {
		return ""
	}
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(s.aead.Seal(nonce, nonce, []byte(plain), nil))
}

func (s *Store) open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return "", errors.New("malformed sealed value")
	}
	plain, err := s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], nil)
	return string(plain), err
}

// Session returns a signed cookie value for an account, valid for ttl.
func (s *Store) Session(id string, ttl time.Duration) string {
	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	return id + "." + exp + "." + s.sign(id+"."+exp)
}

// SessionAccount checks a cookie value and returns its account, or nil.
func (s *Store) SessionAccount(value string) *Account {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return nil
	}
	if !hmac.Equal([]byte(parts[2]), []byte(s.sign(parts[0]+"."+parts[1]))) {
		return nil
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return nil
	}
	return s.Get(parts[0])
}

func (s *Store) sign(v string) string {
	m := hmac.New(sha256.New, s.mac)
	m.Write([]byte(v))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
