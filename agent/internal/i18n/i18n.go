// Package i18n holds the texts of the tray app and its notifications (locales/*.json, embedded),
// in English and Brazilian Portuguese with the same keys.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Languages and the "follow Windows" choice.
const (
	Auto = "auto"
	EN   = "en"
	PT   = "pt-BR"
)

// Languages lists the supported languages.
var Languages = []string{EN, PT}

//go:embed locales/*.json
var files embed.FS

var tables = load()

func load() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, lang := range Languages {
		data, err := files.ReadFile("locales/" + lang + ".json")
		if err != nil {
			panic("i18n: missing locale " + lang)
		}
		m := map[string]string{}
		if err := json.Unmarshal(data, &m); err != nil {
			panic("i18n: bad locale " + lang + ": " + err.Error())
		}
		out[lang] = m
	}
	return out
}

// FromLocale maps a locale name (pt-BR, pt_PT, en-US...) to a supported language: any Portuguese →
// pt-BR, everything else → en.
func FromLocale(locale string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "pt") {
		return PT
	}
	return EN
}

// Resolve turns a saved preference (auto, en, pt-BR) into a language; system is the Windows
// display language, used for "auto" and anything unknown.
func Resolve(pref, system string) string {
	for _, l := range Languages {
		if pref == l {
			return l
		}
	}
	return FromLocale(system)
}

// T returns the text for key in lang (English when missing), formatted with args.
func T(lang, key string, args ...any) string {
	s, ok := tables[lang][key]
	if !ok {
		s, ok = tables[EN][key]
	}
	if !ok {
		s = key
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// N picks key_one or key_other by n (one only for exactly 1, in both languages) and formats n.
func N(lang, key string, n int) string {
	form := "_other"
	if n == 1 {
		form = "_one"
	}
	return T(lang, key+form, n)
}

// Keys lists a language's keys, sorted (tests).
func Keys(lang string) []string {
	keys := make([]string, 0, len(tables[lang]))
	for k := range tables[lang] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
