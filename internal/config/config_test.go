package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenFromPreferences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Preferences.xml")
	xml := `<?xml version="1.0" encoding="utf-8"?>
<Preferences MachineIdentifier="abc" PlexOnlineUsername="someone" PlexOnlineToken="tok-123" FriendlyName="x"/>`
	if err := os.WriteFile(path, []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := TokenFromPreferences(path)
	if err != nil || got != "tok-123" {
		t.Fatalf("got %q, %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`<Preferences FriendlyName="x"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := TokenFromPreferences(path); err == nil {
		t.Fatal("a server not signed in must be reported")
	}
}

func TestLoadReadsTokenFromPreferences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Preferences.xml")
	if err := os.WriteFile(path, []byte(`<Preferences PlexOnlineToken="from-file"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLEX_TOKEN", "")
	t.Setenv("PLEX_PREFERENCES", path)
	c, err := Load()
	if err != nil || c.PlexToken != "from-file" {
		t.Fatalf("got %q, %v", c.PlexToken, err)
	}
	t.Setenv("PLEX_TOKEN", "explicit")
	if c, _ = Load(); c.PlexToken != "explicit" {
		t.Fatal("PLEX_TOKEN must win over the preferences file")
	}
	t.Setenv("PLEX_TOKEN", "")
	t.Setenv("PLEX_PREFERENCES", filepath.Join(dir, "missing.xml"))
	if c, err := Load(); err != nil || c.PlexToken != "" {
		t.Fatalf("without Plex config the panel sign-in is used instead: %q %v", c.PlexToken, err)
	}
	if err := os.WriteFile(path, []byte(`<Preferences FriendlyName="x"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLEX_PREFERENCES", path)
	if _, err := Load(); err == nil {
		t.Fatal("a Plex config without a signed-in account must be reported")
	}
}

func TestLanguageDefaults(t *testing.T) {
	t.Setenv("PLEX_TOKEN", "x")
	t.Setenv("LANGUAGES", "")
	t.Setenv("LANGUAGE", "")
	c, _ := Load()
	if c.Language != "es" || strings.Join(c.Languages, ",") != "castellano,latino,ingles" {
		t.Fatalf("default: %q %v", c.Language, c.Languages)
	}
	t.Setenv("LANGUAGE", "en_US.UTF-8")
	c, _ = Load()
	if c.Language != "en" || strings.Join(c.Languages, ",") != "ingles" {
		t.Fatalf("english: %q %v", c.Language, c.Languages)
	}
	t.Setenv("LANGUAGES", "japones,ingles")
	if c, _ = Load(); strings.Join(c.Languages, ",") != "japones,ingles" {
		t.Fatalf("LANGUAGES must win: %v", c.Languages)
	}
	t.Setenv("LANGUAGE", "fr")
	if c, _ = Load(); c.Language != "es" {
		t.Fatalf("unsupported languages fall back to Spanish: %q", c.Language)
	}
}
