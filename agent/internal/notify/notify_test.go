package notify

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/outbox"
)

// ---- queue ----

func trophies(grades ...string) []Item {
	var out []Item
	for i, g := range grades {
		out = append(out, Item{Grade: g, Trophy: string(rune('A' + i))})
	}
	return out
}

func drain(q *Queue) []Item {
	var out []Item
	for {
		it, ok := q.Next()
		if !ok {
			return out
		}
		out = append(out, it)
	}
}

func TestQueueShowsUpToThreeInOrder(t *testing.T) {
	var q Queue
	q.Add(trophies(Bronze, Silver)...)
	q.Add(trophies(Gold)...)
	got := drain(&q)
	if len(got) != 3 || got[0].Grade != Bronze || got[2].Grade != Gold {
		t.Fatalf("got %+v", got)
	}
}

func TestQueueMergesBurstsButKeepsPlatinum(t *testing.T) {
	var q Queue
	q.Add(trophies(Bronze, Bronze, Silver, Platinum, Gold)...)
	got := drain(&q)
	if len(got) != 2 || !got[0].IsPlatinum() || !got[1].Summary || got[1].Count != 4 {
		t.Fatalf("want platinum + summary of 4, got %+v", got)
	}
	if got[1].Icon == nil {
		t.Fatal("summary has an icon")
	}
}

func TestQueueMergesWhatIsStillWaiting(t *testing.T) {
	var q Queue
	q.Add(trophies(Bronze, Bronze)...)
	first, _ := q.Next() // on screen: no longer merged
	q.Add(trophies(Silver, Silver, Gold)...)
	q.Add(trophies(Bronze)...) // summary grows instead of a second summary
	got := drain(&q)
	if first.Grade != Bronze || len(got) != 1 || !got[0].Summary || got[0].Count != 5 {
		t.Fatalf("first=%+v rest=%+v", first, got)
	}
}

func TestQueueAddInOrderNeverMerges(t *testing.T) {
	var q Queue
	q.AddInOrder(Samples("pt-BR")...)
	q.AddInOrder(SampleSummary())
	got := drain(&q)
	if len(got) != 5 || got[3].Grade != Platinum || !got[4].Summary {
		t.Fatalf("got %+v", got)
	}
}

// ---- texts ----

func TestTexts(t *testing.T) {
	gold := Item{Grade: Gold, Trophy: "Caçador", Game: "Bloodborne"}
	if Label("pt-BR", gold) != "Troféu de ouro" || Title("pt-BR", gold) != "Caçador" || Subtitle("pt-BR", gold) != "Bloodborne" {
		t.Fatal("pt-BR card")
	}
	if Label("en", Item{Grade: Platinum}) != "Platinum earned!" {
		t.Fatal("platinum label")
	}
	sum := Item{Summary: true, Count: 7}
	if Title("pt-BR", sum) != "7 troféus desbloqueados" || Subtitle("en", sum) != "Synced with your profile." {
		t.Fatal("summary")
	}
	if NormalizeGrade("GOLD ") != Gold || NormalizeGrade("diamond") != Bronze {
		t.Fatal("grades")
	}
	for _, g := range Grades {
		if ValidatePNG(GenericIcon(g)) != nil {
			t.Fatalf("built-in %s icon must be a valid PNG", g)
		}
	}
	if !strings.HasPrefix(string(ChimeWAV[:4]), "RIFF") || !strings.HasPrefix(string(PlatinumWAV[:4]), "RIFF") {
		t.Fatal("sounds are WAV files")
	}
}

// ---- icon cache ----

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(1, 1, color.NRGBA{200, 160, 90, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type iconServer struct {
	srv   *httptest.Server
	hits  atomic.Int32
	files map[string][]byte
}

func newIconServer(t *testing.T) *iconServer {
	s := &iconServer{files: map[string][]byte{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://evil.example/x.png", http.StatusFound)
			return
		}
		data, ok := s.files[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func TestIconCacheAcceptsOnlyValidPNGsFromTheServer(t *testing.T) {
	s := newIconServer(t)
	good := pngBytes(t, 64, 64)
	s.files["/api/catalog/NPWR05818_00/icons/1.png"] = good
	s.files["/fake.png"] = []byte("\x89PNG\r\n\x1a\nnot really a png")
	s.files["/huge.png"] = pngBytes(t, 2000, 10)
	s.files["/heavy.png"] = append(append([]byte{}, good...), make([]byte, MaxIconBytes)...)
	dir := t.TempDir()
	c := &IconCache{Dir: dir, Server: s.srv.URL}
	ctx := context.Background()

	data, err := c.Get(ctx, "/api/catalog/NPWR05818_00/icons/1.png")
	if err != nil || !bytes.Equal(data, good) {
		t.Fatalf("valid icon: %v", err)
	}
	before := s.hits.Load()
	if _, err := c.Get(ctx, "/api/catalog/NPWR05818_00/icons/1.png"); err != nil || s.hits.Load() != before {
		t.Fatal("second time comes from the cache")
	}
	for _, bad := range []string{"/fake.png", "/huge.png", "/heavy.png", "/missing.png"} {
		if _, err := c.Get(ctx, bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	for _, foreign := range []string{"https://evil.example/icon.png", "//evil.example/icon.png",
		"http://user:pw@" + strings.TrimPrefix(s.srv.URL, "http://") + "/x.png", "file:///C:/Windows/win.ini"} {
		if _, err := c.Get(ctx, foreign); !errors.Is(err, ErrForeignURL) {
			t.Errorf("%s: want ErrForeignURL, got %v", foreign, err)
		}
	}
	if _, err := c.Get(ctx, "/redirect"); err == nil {
		t.Error("a redirect to another domain must be refused")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("only the valid icon is cached, found %d files", len(entries))
	}
	// A file broken on disk is dropped and fetched again.
	os.WriteFile(filepath.Join(dir, entries[0].Name()), []byte("junk"), 0o600)
	if data, err := c.Get(ctx, "/api/catalog/NPWR05818_00/icons/1.png"); err != nil || !bytes.Equal(data, good) {
		t.Fatal("broken cache entry is replaced")
	}
}

func TestIconCacheStaysUnderItsLimit(t *testing.T) {
	dir := t.TempDir()
	c := &IconCache{Dir: dir, Server: "http://127.0.0.1"}
	big := make([]byte, 6<<20)
	old := time.Now().Add(-time.Hour)
	for i, name := range []string{"a.png", "b.png", "c.png", "d.png"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, big, 0o600)
		t0 := old.Add(time.Duration(i) * time.Minute)
		os.Chtimes(p, t0, t0)
	}
	c.trim()
	if _, err := os.Stat(filepath.Join(dir, "a.png")); !os.IsNotExist(err) {
		t.Fatal("the oldest icon goes first")
	}
	if _, err := os.Stat(filepath.Join(dir, "d.png")); err != nil {
		t.Fatal("the newest stays")
	}
}

// ---- builder ----

type fakeGames struct {
	calls int
	err   error
	langs []string
}

func (f *fakeGames) GameInfo(_ context.Context, id, lang string) (*api.GameInfo, error) {
	f.calls++
	f.langs = append(f.langs, lang)
	if f.err != nil {
		return nil, f.err
	}
	name := "Bloodborne"
	if lang == "pt-BR" {
		name = "Bloodborne (pt)"
	}
	return &api.GameInfo{Slug: "bloodborne", Name: name, SetID: "NPWR05818_00", Trophies: map[int]api.TrophyInfo{
		0: {Name: "Bloodborne", Grade: "platinum", IconURL: "/icons/0.png"},
		5: {Name: "Caçador", Grade: "gold", IconURL: "https://evil.example/5.png"},
		9: {Hidden: true, Grade: "silver"},
	}}, nil
}

type fakeIcons struct{ data map[string][]byte }

func (f fakeIcons) Get(_ context.Context, raw string) ([]byte, error) {
	if d, ok := f.data[raw]; ok {
		return d, nil
	}
	return nil, ErrForeignURL
}

func TestBuilder(t *testing.T) {
	games := &fakeGames{}
	icon := []byte("server icon")
	b := &Builder{Games: games, Icons: fakeIcons{map[string][]byte{"/icons/0.png": icon}}, Lang: "pt-BR",
		SiteURL: "https://vigil.example"}
	items := b.Items(context.Background(), []outbox.Event{
		{TrophySetID: "NPWR05818_00", TrophyID: 0},
		{TrophySetID: "NPWR05818_00", TrophyID: 5},
		{TrophySetID: "NPWR05818_00", TrophyID: 9},
	})
	p, g, h := items[0], items[1], items[2]
	if !p.IsPlatinum() || !bytes.Equal(p.Icon, icon) || p.URL != "https://vigil.example/games/bloodborne?set=NPWR05818_00" {
		t.Fatalf("platinum: %+v", p)
	}
	if g.Trophy != "Caçador" || g.Game != "Bloodborne (pt)" || !bytes.Equal(g.Icon, GenericIcon(Gold)) {
		t.Fatalf("an icon that cannot be fetched falls back to the grade's: %+v", g)
	}
	if h.Trophy != "Troféu #9" || h.Grade != Silver {
		t.Fatalf("hidden trophy: %+v", h)
	}
	if games.calls != 1 {
		t.Fatalf("one lookup per game, got %d", games.calls)
	}

	offline := &Builder{Games: &fakeGames{err: errors.New("offline")}, Lang: "en"}
	it := offline.Item(context.Background(), outbox.Event{TitleID: "CUSA03173", TrophyID: 3})
	if it.Trophy != "Trophy #3" || it.URL != "" || !bytes.Equal(it.Icon, GenericIcon(Bronze)) {
		t.Fatalf("offline: %+v", it)
	}
	noSite := &Builder{Games: &fakeGames{}, Lang: "en"}
	if noSite.Item(context.Background(), outbox.Event{TrophySetID: "NPWR05818_00", TrophyID: 5}).URL != "" {
		t.Fatal("no site address, nothing to open")
	}
}

func TestBuilderBacksOffAfterAFailure(t *testing.T) {
	games := &fakeGames{err: errors.New("offline")}
	now := time.Unix(0, 0)
	b := &Builder{Games: games, Lang: "en", Now: func() time.Time { return now }}
	e := outbox.Event{TrophySetID: "NPWR00001_00", TrophyID: 1}
	b.Item(context.Background(), e)
	b.Item(context.Background(), e)
	if games.calls != 1 {
		t.Fatalf("calls=%d", games.calls)
	}
	now = now.Add(11 * time.Minute)
	b.Item(context.Background(), e)
	if games.calls != 2 {
		t.Fatal("asks again after the back-off")
	}
}

func TestBuilderCachesPerLanguage(t *testing.T) {
	games := &fakeGames{}
	b := &Builder{Games: games, Lang: "pt-BR"}
	e := outbox.Event{TrophySetID: "NPWR05818_00", TrophyID: 5}
	if b.Item(context.Background(), e).Game != "Bloodborne (pt)" {
		t.Fatal("pt-BR name")
	}
	b.SetLang("en")
	if it := b.Item(context.Background(), e); it.Game != "Bloodborne" {
		t.Fatalf("after switching to English: %+v", it.Game)
	}
	b.Item(context.Background(), e)
	if games.calls != 2 || games.langs[0] != "pt-BR" || games.langs[1] != "en" {
		t.Fatalf("one lookup per language: %v", games.langs)
	}
	off := Item{Grade: Bronze, Trophy: T("en", "trophy_n", 3)}
	if off.Trophy != "Trophy #3" {
		t.Fatal(off.Trophy)
	}
}

func TestVolume(t *testing.T) {
	if Sound(false, "off") != nil {
		t.Fatal("off is silent")
	}
	low, high := Sound(false, "low"), Sound(false, "high")
	if len(low) != len(ChimeWAV) || len(high) != len(ChimeWAV) {
		t.Fatal("same length as the original")
	}
	peak := func(w []byte) int {
		p := 0
		for i := 44; i+1 < len(w); i += 2 {
			v := int(int16(uint16(w[i]) | uint16(w[i+1])<<8))
			if v < 0 {
				v = -v
			}
			p = max(p, v)
		}
		return p
	}
	if !(peak(low) < peak(Sound(false, "medium")) && peak(Sound(false, "medium")) < peak(high)) {
		t.Fatal("louder at each step")
	}
	// The file itself is quiet: at most about -12 dBFS.
	if peak(ChimeWAV) > 8500 || peak(PlatinumWAV) > 8500 {
		t.Fatalf("sounds must be normalized low, peaks %d / %d", peak(ChimeWAV), peak(PlatinumWAV))
	}
	if peak(low) > 3500 {
		t.Fatalf("low is soft, peak %d", peak(low))
	}
	if _, err := ScaleWAV([]byte("not a wav file at all"), 0.5); err == nil {
		t.Fatal("junk refused")
	}
}

func TestUpdateNoticeIsItsOwnQuietCard(t *testing.T) {
	up := UpdateItem("v0.1.0-beta.10", "https://github.com/guutol/vigil-agent-releases/releases/download/v0.1.0-beta.10/vigil-agent-tray.exe")
	if !up.Silent() || up.IsPlatinum() || len(up.Icon) == 0 {
		t.Fatalf("update card: %+v", up)
	}
	for lang, want := range map[string][3]string{
		"pt-BR": {"Nova versão do Vigil disponível", "A versão v0.1.0-beta.10 está disponível. Clique para baixar.", "Vigil Trophies"},
		"en":    {"New Vigil version available", "Version v0.1.0-beta.10 is available. Click to download.", "Vigil Trophies"},
	} {
		got := [3]string{Label(lang, up), Title(lang, up), Subtitle(lang, up)}
		if got != want {
			t.Errorf("%s: %q", lang, got)
		}
	}
	// a burst of trophies becomes a summary, but the update notice keeps its own card
	var q Queue
	q.Add(up)
	q.Add(Item{Grade: Bronze, Trophy: "a"}, Item{Grade: Bronze, Trophy: "b"}, Item{Grade: Bronze, Trophy: "c"}, Item{Grade: Bronze, Trophy: "d"})
	first, _ := q.Next()
	second, _ := q.Next()
	if !first.Update || !second.Summary || second.Count != 4 || q.Len() != 0 {
		t.Fatalf("queue: %+v then %+v (%d left)", first, second, q.Len())
	}
	if (Item{Grade: Gold}).Silent() {
		t.Error("trophies keep their chime")
	}
}
