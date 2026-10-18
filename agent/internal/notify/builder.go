package notify

import (
	"context"
	"net/url"
	"sync"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/outbox"
)

// GameSource reads a game's public page (api.Client.GameInfo).
type GameSource interface {
	GameInfo(ctx context.Context, setOrTitleID, lang string) (*api.GameInfo, error)
}

// UnlockedTrophySource resolves concealed metadata only for server-confirmed ownership.
type UnlockedTrophySource interface {
	UnlockedTrophy(ctx context.Context, setOrTitleID string, trophyID int, lang string) (*api.UnlockedTrophy, error)
}

// IconSource gives an icon's PNG (IconCache.Get).
type IconSource interface {
	Get(ctx context.Context, raw string) ([]byte, error)
}

// Builder turns delivered unlocks into cards: trophy name, grade, game, icon and game page. It
// caches each game for the session and does not ask again for a while after a failure.
type Builder struct {
	Games GameSource
	Icons IconSource
	Lang  string
	// SiteURL is the website (from /api/agent/me) for the "open the game page" click; may be empty.
	SiteURL string
	Now     func() time.Time

	mu    sync.Mutex
	games map[string]*api.GameInfo
	fails map[string]time.Time
}

// retryAfter is how long a failed game lookup is not repeated.
const retryAfter = 10 * time.Minute

func (b *Builder) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// SetLang changes the language of the next cards (names are cached per language).
func (b *Builder) SetLang(lang string) {
	b.mu.Lock()
	b.Lang = lang
	b.mu.Unlock()
}

func (b *Builder) language() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Lang
}

func (b *Builder) game(ctx context.Context, set, lang string) *api.GameInfo {
	if set == "" || b.Games == nil {
		return nil
	}
	key := lang + "|" + set
	b.mu.Lock()
	if b.games == nil {
		b.games, b.fails = map[string]*api.GameInfo{}, map[string]time.Time{}
	}
	g, ok := b.games[key]
	failedAt, failed := b.fails[key]
	b.mu.Unlock()
	if ok {
		return g
	}
	if failed && b.now().Sub(failedAt) < retryAfter {
		return nil
	}
	lookup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	g, err := b.Games.GameInfo(lookup, set, lang)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.fails[key] = b.now()
		return nil
	}
	b.games[key] = g
	return g
}

// Item builds the card of one unlock. Without the server's data it still works: "Trophy #N"
// with the generic bronze icon.
func (b *Builder) Item(ctx context.Context, e outbox.Event) Item {
	key := e.TrophySetID
	if key == "" {
		key = e.TitleID
	}
	lang := b.language()
	it := Item{Grade: Bronze, Trophy: T(lang, "trophy_n", e.TrophyID)}
	g := b.game(ctx, key, lang)
	var t api.TrophyInfo
	known := false
	if g != nil {
		it.Game = g.Name
		t, known = g.Trophies[e.TrophyID]
	}
	if !known || t.Hidden || t.Name == "" {
		if owned, ok := b.Games.(UnlockedTrophySource); ok {
			lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
			details, err := owned.UnlockedTrophy(lookup, key, e.TrophyID, lang)
			cancel()
			if err == nil && details != nil {
				// Do not put private names in b.games: its public cache can outlive an account link.
				t, known = details.TrophyInfo, true
				it.Game = details.Game
				g = &api.GameInfo{Name: details.Game, Slug: details.Slug, SetID: details.SetID}
			}
		}
	}
	if known {
		it.Grade = NormalizeGrade(t.Grade)
		if t.Name != "" {
			it.Trophy = t.Name
		}
	}
	if known && t.IconURL != "" && b.Icons != nil {
		if data, err := b.Icons.Get(ctx, t.IconURL); err == nil {
			it.Icon = data
		}
	}
	if it.Icon == nil {
		it.Icon = GenericIcon(it.Grade)
	}
	if g != nil {
		it.URL = b.gameURL(g)
	}
	return it
}

// Items builds the cards of a batch of delivered unlocks, in order.
func (b *Builder) Items(ctx context.Context, events []outbox.Event) []Item {
	out := make([]Item, 0, len(events))
	for _, e := range events {
		out = append(out, b.Item(ctx, e))
	}
	return out
}

// gameURL is <site>/games/<slug>?set=<id>, or "" without a site address.
func (b *Builder) gameURL(g *api.GameInfo) string {
	if b.SiteURL == "" || g.Slug == "" {
		return ""
	}
	site, err := url.Parse(b.SiteURL)
	if err != nil || site.Host == "" {
		return ""
	}
	u := site.JoinPath("games", g.Slug)
	if g.SetID != "" {
		u.RawQuery = url.Values{"set": {g.SetID}}.Encode()
	}
	return u.String()
}
