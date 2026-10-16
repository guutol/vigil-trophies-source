// Package notify turns delivered unlocks into trophy notifications: the "Vigil style" popup (its
// own always-on-top window, Windows only), a Windows notification from the tray icon, or nothing.
// This file and the others without a build tag are the testable, cross-platform part: items,
// the queue, the icon cache, texts and the embedded assets.
package notify

import (
	_ "embed"
	"strings"

	"github.com/vigil-tracker/agent/internal/i18n"
)

// Grades, as the server names them.
const (
	Bronze   = "bronze"
	Silver   = "silver"
	Gold     = "gold"
	Platinum = "platinum"
)

// Grades in display order.
var Grades = []string{Bronze, Silver, Gold, Platinum}

// Item is one notification.
type Item struct {
	// Summary: a burst of unlocks shown as one card ("5 trophies unlocked"); Count says how many.
	Summary bool
	Count   int

	Grade  string // bronze, silver, gold, platinum ("" for a summary)
	Trophy string // trophy name ("Trophy #N" when the server does not know it)
	Game   string // game name ("" when unknown)
	Icon   []byte // PNG; the generic icon of the grade when the server has none
	URL    string // game page on the website, opened on click ("" = nothing to open)

	// Update: not a trophy but "a new Vigil is out" (Version), with the Vigil mark instead of a
	// trophy, no chime, and URL = the official download. Never a summary, never merged.
	Update  bool
	Version string
}

// UpdateItem is the card for a new version; url is the official download, already validated.
func UpdateItem(version, url string) Item {
	return Item{Update: true, Version: version, URL: url, Icon: VigilIcon}
}

// IsPlatinum is true for a platinum trophy (shown with its own highlight and sound).
func (it Item) IsPlatinum() bool { return !it.Summary && !it.Update && it.Grade == Platinum }

// Silent: no chime (the update notice is a quiet message, not a trophy).
func (it Item) Silent() bool { return it.Update }

// VigilIcon is the app's mark (the candle with the eye), used on update notices.
//
//go:embed assets/vigil.png
var VigilIcon []byte

//go:embed assets/grade-bronze.png
var iconBronze []byte

//go:embed assets/grade-silver.png
var iconSilver []byte

//go:embed assets/grade-gold.png
var iconGold []byte

//go:embed assets/grade-platinum.png
var iconPlatinum []byte

// ChimeWAV and PlatinumWAV are the notification sounds (original, see assets/make_assets.py).
//
//go:embed assets/chime.wav
var ChimeWAV []byte

//go:embed assets/chime-platinum.wav
var PlatinumWAV []byte

// GenericIcon is the built-in icon of a grade (bronze for anything unknown).
func GenericIcon(grade string) []byte {
	switch grade {
	case Silver:
		return iconSilver
	case Gold:
		return iconGold
	case Platinum:
		return iconPlatinum
	}
	return iconBronze
}

// NormalizeGrade maps the server's value to one of Grades (unknown → bronze).
func NormalizeGrade(g string) string {
	g = strings.ToLower(strings.TrimSpace(g))
	for _, known := range Grades {
		if g == known {
			return g
		}
	}
	return Bronze
}

// ---- texts (internal/i18n/locales) ----

// T returns a text in lang (English when unknown).
func T(lang, key string, args ...any) string { return i18n.T(lang, key, args...) }

// Label is the first line of a card: the grade, or "Vigil Trophies" for a summary.
func Label(lang string, it Item) string {
	if it.Update {
		return T(lang, "update_title") // "New Vigil version available"
	}
	if it.Summary {
		return "Vigil Trophies"
	}
	return T(lang, "label_"+NormalizeGrade(it.Grade))
}

// Title is the main line: the trophy name, or "N trophies unlocked".
func Title(lang string, it Item) string {
	if it.Update {
		return T(lang, "update_body", it.Version) // "Version X is available. Click to download." (two lines fit)
	}
	if it.Summary {
		return T(lang, "summary", it.Count)
	}
	return it.Trophy
}

// Subtitle is the last line: the game, or "Synced with your profile.".
func Subtitle(lang string, it Item) string {
	if it.Update {
		return "Vigil Trophies"
	}
	if it.Summary || it.Game == "" {
		return T(lang, "summary_body")
	}
	return it.Game
}

// Samples is one example trophy of each grade (the "Test notification" menu item); nothing is sent.
func Samples(lang string) []Item {
	out := make([]Item, 0, len(Grades))
	for _, g := range Grades {
		out = append(out, Item{Grade: g, Trophy: T(lang, "sample_"+g), Game: T(lang, "sample_game"), Icon: GenericIcon(g)})
	}
	return out
}

// SampleSummary is an example of the burst summary.
func SampleSummary() Item {
	return Item{Summary: true, Count: 5, Icon: GenericIcon(Gold)}
}
