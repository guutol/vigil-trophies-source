package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	order := []string{
		"v0.1.0-alpha", "v0.1.0-beta", "v0.1.0-beta.2", "v0.1.0-beta.9", "v0.1.0-beta.10",
		"v0.1.0-beta.10.1", "v0.1.0-rc.1", "v0.1.0", "v0.1.1-beta.1", "v0.1.1", "v0.2.0", "v1.0.0", "v10.0.0",
	}
	for i := range order {
		for j := range order {
			a, ok1 := Parse(order[i])
			b, ok2 := Parse(order[j])
			if !ok1 || !ok2 {
				t.Fatalf("cannot parse %s or %s", order[i], order[j])
			}
			want := sign(i - j)
			if got := Compare(a, b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"", "dev", "v1", "v1.2", "v1.2.3.4", "v01.2.3", "v1.2.x", "v1.2.3-", "v1.2.3-beta..1", "vv1.2.3"} {
		if _, ok := Parse(s); ok {
			t.Errorf("Parse(%q) accepted", s)
		}
	}
	if v, ok := Parse("1.2.3+build.5"); !ok || v.Patch != 3 || len(v.Pre) != 0 {
		t.Errorf("build metadata: %+v %v", v, ok)
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		cand, cur string
		want      bool
	}{
		{"v0.1.0-beta.10", "v0.1.0-beta.9", true},
		{"v0.1.0-beta.9", "v0.1.0-beta.10", false},
		{"v0.1.0", "v0.1.0-beta.10", true},
		{"v0.1.0-beta.6", "v0.1.0-beta.6", false},
		{"v0.1.0-beta.7", "dev", false}, // a development build is never told to update
		{"garbage", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.cand, c.cur); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.cand, c.cur, got)
		}
	}
}

func TestAssetFor(t *testing.T) {
	cases := map[[3]string]string{
		{"windows", "amd64", "tray"}: "vigil-agent-tray.exe",
		{"windows", "amd64", ""}:     "vigil-agent-windows-amd64.exe",
		{"linux", "amd64", ""}:       "vigil-agent-linux-amd64",
		{"linux", "arm64", ""}:       "vigil-agent-linux-arm64",
		{"darwin", "arm64", ""}:      "",
		{"windows", "arm64", "tray"}: "",
	}
	for in, want := range cases {
		if got := AssetFor(in[0], in[1], in[2] == "tray"); got != want {
			t.Errorf("AssetFor%v = %q, want %q", in, got, want)
		}
	}
}

func TestValidDownloadURL(t *testing.T) {
	const tag, file = "v0.1.0-beta.7", "vigil-agent-tray.exe"
	good := "https://github.com/guutol/vigil-agent-releases/releases/download/v0.1.0-beta.7/vigil-agent-tray.exe"
	if !ValidDownloadURL(good, tag, file) {
		t.Fatal("official URL refused")
	}
	for _, bad := range []string{
		"http://github.com/guutol/vigil-agent-releases/releases/download/v0.1.0-beta.7/vigil-agent-tray.exe",
		"https://github.com.evil.example/guutol/vigil-agent-releases/releases/download/v0.1.0-beta.7/vigil-agent-tray.exe",
		"https://github.com:444/guutol/vigil-agent-releases/releases/download/v0.1.0-beta.7/vigil-agent-tray.exe",
		"https://user@github.com/guutol/vigil-agent-releases/releases/download/v0.1.0-beta.7/vigil-agent-tray.exe",
		"https://github.com/guutol/vigil-trophies/releases/download/v0.1.0-beta.7/vigil-agent-tray.exe",
		"https://github.com/someone/vigil-agent-releases/releases/download/v0.1.0-beta.7/vigil-agent-tray.exe",
		"https://github.com/guutol/vigil-agent-releases/releases/download/v0.1.0-beta.6/vigil-agent-tray.exe",
		"https://github.com/guutol/vigil-agent-releases/releases/download/v0.1.0-beta.7/vigil-agent-linux-amd64",
		good + "?x=1",
		good + "#x",
		"https://github.com/guutol/vigil-agent-releases/releases/download/v0.1.0-beta.7/../../evil/vigil-agent-tray.exe",
		"javascript:alert(1)",
	} {
		if ValidDownloadURL(bad, tag, file) {
			t.Errorf("accepted %s", bad)
		}
	}
}

// fakeGitHub serves a release list and counts requests; status overrides the answer.
type fakeGitHub struct {
	body     string
	etag     string
	status   int
	requests atomic.Int32
	lastINM  atomic.Value
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	f.lastINM.Store(r.Header.Get("If-None-Match"))
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	if f.etag != "" && r.Header.Get("If-None-Match") == f.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if f.etag != "" {
		w.Header().Set("ETag", f.etag)
	}
	_, _ = w.Write([]byte(f.body))
}

func dl(tag, file string) string {
	return fmt.Sprintf("https://github.com/guutol/vigil-agent-releases/releases/download/%s/%s", tag, file)
}

func releasesJSON(rels ...string) string { return "[" + strings.Join(rels, ",") + "]" }

func rel(tag string, draft bool, files ...string) string {
	var assets []string
	for _, f := range files {
		assets = append(assets, fmt.Sprintf(`{"name":%q,"browser_download_url":%q,"state":"uploaded"}`, f, dl(tag, f)))
	}
	return fmt.Sprintf(`{"tag_name":%q,"draft":%v,"prerelease":true,"assets":[%s]}`, tag, draft, strings.Join(assets, ","))
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newChecker(t *testing.T, srv *httptest.Server, current string, clk *clock) *Checker {
	t.Helper()
	load, save := FileState(filepath.Join(t.TempDir(), "update.json"))
	return &Checker{Current: current, Asset: "vigil-agent-tray.exe", API: srv.URL, Client: srv.Client(),
		Now: clk.now, Load: load, Save: save}
}

func TestCheckPicksNewestBetaWithThisPlatformsFile(t *testing.T) {
	gh := &fakeGitHub{body: releasesJSON(
		rel("v0.1.0-beta.9", false, "vigil-agent-tray.exe"),
		rel("v0.1.0-beta.11", true, "vigil-agent-tray.exe"),     // draft: ignored
		rel("v0.1.0-beta.12", false, "vigil-agent-linux-amd64"), // no tray file: ignored
		rel("v0.1.0-beta.10", false, "vigil-agent-tray.exe", "vigil-agent-linux-amd64"),
		`{"tag_name":"v0.1.0-beta.13","assets":[{"name":"vigil-agent-tray.exe","browser_download_url":"https://evil.example/x.exe","state":"uploaded"}]}`,
		`{"tag_name":"v0.1.0-beta.14","assets":[{"name":"vigil-agent-tray.exe","browser_download_url":"`+dl("v0.1.0-beta.14", "vigil-agent-tray.exe")+`","state":"new"}]}`,
		rel("nightly", false, "vigil-agent-tray.exe"),
	)}
	srv := httptest.NewServer(gh)
	defer srv.Close()
	c := newChecker(t, srv, "v0.1.0-beta.9", &clock{time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)})
	res, err := c.Check(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Latest != "v0.1.0-beta.10" || res.URL != dl("v0.1.0-beta.10", "vigil-agent-tray.exe") {
		t.Fatalf("got %+v", res)
	}
}

func TestCheckUsesTheCacheAndTheETag(t *testing.T) {
	gh := &fakeGitHub{body: releasesJSON(rel("v0.1.0-beta.7", false, "vigil-agent-tray.exe")), etag: `"abc"`}
	srv := httptest.NewServer(gh)
	defer srv.Close()
	clk := &clock{time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	c := newChecker(t, srv, "v0.1.0-beta.6", clk)
	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	// within CheckEvery: answered from the saved state, no request
	clk.t = clk.t.Add(5 * time.Hour)
	res, err := c.Check(context.Background(), false)
	if err != nil || !res.Cached || !res.Available || gh.requests.Load() != 1 {
		t.Fatalf("cached check: %+v %v, %d requests", res, err, gh.requests.Load())
	}
	// after it: asks again with If-None-Match; a 304 keeps the answer
	clk.t = clk.t.Add(2 * time.Hour)
	res, err = c.Check(context.Background(), false)
	if err != nil || res.Cached || res.Latest != "v0.1.0-beta.7" || gh.requests.Load() != 2 || gh.lastINM.Load() != `"abc"` {
		t.Fatalf("revalidated check: %+v %v, %d requests, INM %v", res, err, gh.requests.Load(), gh.lastINM.Load())
	}
	// a manual check always asks
	if _, err := c.Check(context.Background(), true); err != nil || gh.requests.Load() != 3 {
		t.Fatalf("forced check: %v, %d requests", err, gh.requests.Load())
	}
}

func TestCheckOfflineKeepsTheLastAnswerAndBacksOff(t *testing.T) {
	gh := &fakeGitHub{body: releasesJSON(rel("v0.1.0-beta.7", false, "vigil-agent-tray.exe"))}
	srv := httptest.NewServer(gh)
	clk := &clock{time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	c := newChecker(t, srv, "v0.1.0-beta.6", clk)
	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	srv.Close() // offline from now on
	clk.t = clk.t.Add(7 * time.Hour)
	res, err := c.Check(context.Background(), false)
	if err == nil {
		t.Fatal("no error while offline")
	}
	if !res.Available || res.Latest != "v0.1.0-beta.7" {
		t.Fatalf("lost the saved answer: %+v", res)
	}
	// the failure is not retried for RetryAfterFailure
	clk.t = clk.t.Add(30 * time.Minute)
	if res, err := c.Check(context.Background(), false); err != nil || !res.Cached {
		t.Fatalf("retried too soon: %+v %v", res, err)
	}
}

func TestCheckRateLimitedIsAnErrorNotAnUpdate(t *testing.T) {
	gh := &fakeGitHub{status: http.StatusForbidden}
	srv := httptest.NewServer(gh)
	defer srv.Close()
	c := newChecker(t, srv, "v0.1.0-beta.6", &clock{time.Now()})
	res, err := c.Check(context.Background(), true)
	if err == nil || res.Available {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestCheckTimesOut(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	c := newChecker(t, srv, "v0.1.0-beta.6", &clock{time.Now()})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Check(ctx, true); !errors.Is(err, context.DeadlineExceeded) && err == nil {
		t.Fatalf("no timeout: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("waited too long")
	}
}

func TestNotifyOncePerVersion(t *testing.T) {
	gh := &fakeGitHub{body: releasesJSON(rel("v0.1.0-beta.7", false, "vigil-agent-tray.exe"))}
	srv := httptest.NewServer(gh)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "update.json")
	load, save := FileState(path)
	clk := &clock{time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	c := &Checker{Current: "v0.1.0-beta.6", Asset: "vigil-agent-tray.exe", API: srv.URL, Client: srv.Client(),
		Now: clk.now, Load: load, Save: save}
	res, _ := c.Check(context.Background(), false)
	if !c.ShouldNotify(res) {
		t.Fatal("first time: should notify")
	}
	if err := c.MarkNotified(res.Latest); err != nil {
		t.Fatal(err)
	}
	// a restart (a new checker on the same file) does not notify the same version again
	c2 := &Checker{Current: "v0.1.0-beta.6", Asset: "vigil-agent-tray.exe", API: srv.URL, Client: srv.Client(),
		Now: clk.now, Load: load, Save: save}
	res2, _ := c2.Check(context.Background(), false)
	if c2.ShouldNotify(res2) {
		t.Fatal("same version notified twice")
	}
	// a newer one is announced again
	gh.body = releasesJSON(rel("v0.1.0-beta.10", false, "vigil-agent-tray.exe"))
	res3, _ := c2.Check(context.Background(), true)
	if !c2.ShouldNotify(res3) {
		t.Fatalf("newer version not announced: %+v", res3)
	}
}

func TestUpToDateAndDevBuilds(t *testing.T) {
	gh := &fakeGitHub{body: releasesJSON(rel("v0.1.0-beta.7", false, "vigil-agent-tray.exe"))}
	srv := httptest.NewServer(gh)
	defer srv.Close()
	for _, cur := range []string{"v0.1.0-beta.7", "v0.1.0-beta.8", "dev"} {
		c := newChecker(t, srv, cur, &clock{time.Now()})
		res, err := c.Check(context.Background(), true)
		if err != nil || res.Available || c.ShouldNotify(res) {
			t.Errorf("%s: %+v %v", cur, res, err)
		}
	}
}

func TestDamagedStateFileIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.json")
	load, save := FileState(path)
	if err := save(State{Latest: "v0.1.0-beta.7", URL: "https://evil.example/x.exe"}); err != nil {
		t.Fatal(err)
	}
	c := &Checker{Current: "v0.1.0-beta.6", Asset: "vigil-agent-tray.exe", Load: load, Save: save,
		Now: func() time.Time { return time.Now() }}
	st := load()
	st.CheckedAt = time.Now()
	_ = save(st)
	res, _ := c.Check(context.Background(), false)
	if res.Available || res.URL != "" {
		t.Fatalf("a saved URL outside the official repository was offered: %+v", res)
	}
}
