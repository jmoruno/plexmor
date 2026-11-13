package accounts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTokensAreEncryptedAndSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	s, err := Open(path, []byte("secret-1"))
	if err != nil {
		t.Fatal(err)
	}
	a := &Account{ID: "u1", PlexUUID: "uuid", Username: "ana", PlexToken: "plex-token-123", Admin: true}
	a.Prefs.Trakt.AccessToken = "trakt-access"
	if err := s.Put(a); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "plex-token-123") || strings.Contains(string(raw), "trakt-access") {
		t.Fatal("tokens must never be written in clear")
	}
	again, err := Open(path, []byte("secret-1"))
	if err != nil {
		t.Fatal(err)
	}
	got := again.Get("u1")
	if got == nil || got.PlexToken != "plex-token-123" || got.Prefs.Trakt.AccessToken != "trakt-access" || !got.Admin {
		t.Fatalf("reloaded account: %+v", got)
	}
	if _, err := Open(path, []byte("other-secret")); err == nil {
		t.Fatal("a different secret must not decrypt the tokens")
	}
}

func TestSessions(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "a.json"), []byte("k"))
	_ = s.Put(&Account{ID: "u1"})
	v := s.Session("u1", time.Hour)
	if a := s.SessionAccount(v); a == nil || a.ID != "u1" {
		t.Fatal("valid session rejected")
	}
	if s.SessionAccount(strings.Replace(v, "u1", "u2", 1)) != nil {
		t.Fatal("tampered session accepted")
	}
	if s.SessionAccount(s.Session("u1", -time.Second)) != nil {
		t.Fatal("expired session accepted")
	}
}

func TestUpdateDoesNotAlias(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "a.json"), []byte("k"))
	_ = s.Put(&Account{ID: "u1", Prefs: Prefs{Languages: []string{"castellano"}}})
	before := s.Get("u1")
	if _, err := s.Update("u1", func(a *Account) { a.Prefs.Languages[0] = "ingles" }); err != nil {
		t.Fatal(err)
	}
	if before.Prefs.Languages[0] != "castellano" {
		t.Fatal("stored accounts must be immutable")
	}
	if s.Get("u1").Prefs.Languages[0] != "ingles" {
		t.Fatal("update lost")
	}
}
