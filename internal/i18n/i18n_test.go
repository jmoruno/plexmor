package i18n

import (
	"strings"
	"testing"
	"time"
)

func TestEveryTextHasBothLanguages(t *testing.T) {
	for key, m := range messages {
		if m.es == "" || m.en == "" {
			t.Errorf("%s: missing a language", key)
		}
		if strings.Count(m.es, "%") != strings.Count(m.en, "%") {
			t.Errorf("%s: placeholders differ between languages", key)
		}
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]string{"en": EN, "EN-us": EN, "en_US.UTF-8": EN, "es": ES, "es-ES": ES, "fr": "", "": ""} {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
	if Or("fr", ES) != ES || Or("en", ES) != EN {
		t.Error("Or does not fall back")
	}
}

func TestAcceptLanguage(t *testing.T) {
	for in, want := range map[string]string{
		"en-GB,en;q=0.9":          EN,
		"fr-FR,fr;q=0.9,es;q=0.8": ES,
		"de":                      "",
		"":                        "",
	} {
		if got := FromAcceptLanguage(in); got != want {
			t.Errorf("FromAcceptLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestT(t *testing.T) {
	if got := T(EN, "home.owner", "Ana"); got != "shared by Ana" {
		t.Errorf("got %q", got)
	}
	if got := T(ES, "home.owner", "Ana"); got != "de Ana" {
		t.Errorf("got %q", got)
	}
	if got := T(EN, "no.such.key"); got != "no.such.key" {
		t.Errorf("unknown keys should come back as the key, got %q", got)
	}
}

func TestAgo(t *testing.T) {
	five := time.Now().Add(-5 * time.Minute).Unix()
	if got := Ago(EN, five); got != "5 min ago" {
		t.Errorf("got %q", got)
	}
	if got := Ago(ES, five); got != "hace 5 min" {
		t.Errorf("got %q", got)
	}
	if got := Ago(EN, 0); got != "never" {
		t.Errorf("got %q", got)
	}
}
