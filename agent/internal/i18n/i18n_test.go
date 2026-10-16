package i18n

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestBothLanguagesHaveTheSameKeys(t *testing.T) {
	en, pt := Keys(EN), Keys(PT)
	if !slices.Equal(en, pt) {
		for _, k := range en {
			if !slices.Contains(pt, k) {
				t.Errorf("pt-BR misses %q", k)
			}
		}
		for _, k := range pt {
			if !slices.Contains(en, k) {
				t.Errorf("en misses %q", k)
			}
		}
	}
	if len(en) < 50 {
		t.Fatalf("only %d keys", len(en))
	}
}

// The same printf verbs in the same order, so a translation can never break formatting.
func TestPlaceholdersMatch(t *testing.T) {
	verbs := regexp.MustCompile(`%[sd]`)
	for _, k := range Keys(EN) {
		a := strings.Join(verbs.FindAllString(T(EN, k), -1), "")
		b := strings.Join(verbs.FindAllString(T(PT, k), -1), "")
		if a != b {
			t.Errorf("%s: en %q vs pt-BR %q", k, a, b)
		}
		if strings.TrimSpace(T(PT, k)) == "" {
			t.Errorf("%s is empty", k)
		}
	}
}

func TestPlurals(t *testing.T) {
	cases := map[string]string{
		N(PT, "games", 1):     "1 jogo",
		N(PT, "games", 0):     "0 jogos",
		N(PT, "games", 2):     "2 jogos",
		N(PT, "trophies", 1):  "1 troféu",
		N(PT, "trophies", 30): "30 troféus",
		N(EN, "games", 1):     "1 game",
		N(EN, "trophies", 2):  "2 trophies",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestLanguageResolution(t *testing.T) {
	for _, c := range []struct{ pref, system, want string }{
		{Auto, "pt-BR", PT}, {Auto, "pt-PT", PT}, {Auto, "en-US", EN}, {Auto, "de-DE", EN}, {Auto, "", EN},
		{EN, "pt-BR", EN}, {PT, "en-US", PT}, {"klingon", "pt-BR", PT},
	} {
		if got := Resolve(c.pref, c.system); got != c.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", c.pref, c.system, got, c.want)
		}
	}
	if T(EN, "no-such-key") != "no-such-key" {
		t.Fatal("unknown key is shown as the key")
	}
}
