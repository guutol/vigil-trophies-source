package app

import "github.com/vigil-tracker/agent/internal/i18n"

// Lang picks the tray's language from a locale name (pt-BR, pt_PT, en-US...): Portuguese or English.
func Lang(locale string) string { return i18n.FromLocale(locale) }

// Text is the tray's text in one language (internal/i18n/locales).
type Text struct{ Lang string }

// Texts returns the texts of lang (English when unknown).
func Texts(lang string) Text { return Text{Lang: i18n.Resolve(lang, i18n.EN)} }

// T formats one string.
func (t Text) T(key string, args ...any) string { return i18n.T(t.Lang, key, args...) }

// N is a counted text with the right plural: "1 game", "2 games".
func (t Text) N(key string, n int) string { return i18n.N(t.Lang, key, n) }

// Source is the readable name of a source type, with "(beta)" for the emulators.
func (t Text) Source(typ string) string { return t.T("src_" + typ) }

// SourceCounts is "bbport (log) — 1 game, 12 trophies".
func (t Text) SourceCounts(typ string, games, unlocks int) string {
	return t.T("source_line", t.Source(typ), t.N("games", games), t.N("trophies", unlocks))
}
