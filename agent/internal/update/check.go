package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/vigil-tracker/agent/internal/outbox"
)

// The official public distribution repository and the list of its releases. The list endpoint is
// used instead of releases/latest because "latest" leaves pre-releases (the betas) out.
const (
	Repo        = "guutol/vigil-agent-releases"
	ReleasesAPI = "https://api.github.com/repos/" + Repo + "/releases?per_page=30"
)

// Timing: a check at most every CheckEvery (startup and the periodic timer both reuse the cached
// answer before that), a failed check is retried after RetryAfterFailure, and one request never
// waits longer than RequestTimeout.
const (
	CheckEvery        = 6 * time.Hour
	RetryAfterFailure = time.Hour
	RequestTimeout    = 15 * time.Second
	maxBody           = 2 << 20
)

// State is what is remembered between runs (update.json in the settings folder). It holds no
// personal data: release tags, the official download address and timestamps.
type State struct {
	CheckedAt time.Time `json:"checked_at,omitzero"`
	FailedAt  time.Time `json:"failed_at,omitzero"`
	ETag      string    `json:"etag,omitempty"`
	Latest    string    `json:"latest,omitempty"`   // newest release that has a file for this platform
	URL       string    `json:"url,omitempty"`      // its official download, validated
	Notified  string    `json:"notified,omitempty"` // newest version the player was already told about
}

// Result is what a check found.
type Result struct {
	Current   string // the running version
	Latest    string // newest published version with a file for this platform ("" when unknown)
	URL       string // official download of Latest for this platform
	Available bool   // Latest is newer than Current
	Cached    bool   // the answer came from the last check, without asking GitHub
}

// Checker asks GitHub for the releases. Safe for concurrent use.
type Checker struct {
	Current string       // running version (api.Version); "dev" never gets update notices
	Asset   string       // the file for this platform (AssetFor)
	Client  *http.Client // nil: a client with RequestTimeout
	API     string       // "" = ReleasesAPI (tests point it elsewhere)
	Now     func() time.Time
	Load    func() State
	Save    func(State) error

	mu sync.Mutex
}

// ErrUnsupported: no official file for this platform (nothing to offer).
var ErrUnsupported = errors.New("no official download for this platform")

// AssetFor is the release file a player on this platform downloads: the tray app on Windows when
// running the tray, the command-line agent otherwise.
func AssetFor(goos, goarch string, tray bool) string {
	switch {
	case goos == "windows" && goarch == "amd64" && tray:
		return "vigil-agent-tray.exe"
	case goos == "windows" && goarch == "amd64":
		return "vigil-agent-windows-amd64.exe"
	case goos == "linux" && (goarch == "amd64" || goarch == "arm64"):
		return "vigil-agent-linux-" + goarch
	}
	return ""
}

// ValidDownloadURL accepts only the official HTTPS address of asset in release tag:
// https://github.com/guutol/vigil-agent-releases/releases/download/<tag>/<asset>, nothing else
// (no other host, port, user info, query or fragment).
func ValidDownloadURL(raw, tag, asset string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if _, ok := Parse(tag); !ok || asset == "" {
		return false
	}
	return u.EscapedPath() == "/"+Repo+"/releases/download/"+url.PathEscape(tag)+"/"+url.PathEscape(asset)
}

// Check returns the newest version. Unless force, a check done less than CheckEvery ago (or a
// failure less than RetryAfterFailure ago) is answered from the saved state without a request.
// Errors (offline, GitHub limits, odd answers) leave the saved answer as it was.
func (c *Checker) Check(ctx context.Context, force bool) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Asset == "" {
		return Result{Current: c.Current}, ErrUnsupported
	}
	now := c.now()
	st := c.load()
	fresh := !st.CheckedAt.IsZero() && now.Sub(st.CheckedAt) >= 0 && now.Sub(st.CheckedAt) < CheckEvery
	backoff := !st.FailedAt.IsZero() && now.Sub(st.FailedAt) >= 0 && now.Sub(st.FailedAt) < RetryAfterFailure
	if !force && (fresh || backoff) {
		res := c.result(st)
		res.Cached = true
		return res, nil
	}
	latest, url, etag, notModified, err := c.fetch(ctx, st.ETag)
	if err != nil {
		st.FailedAt = now
		_ = c.save(st)
		return c.result(st), err
	}
	st.CheckedAt, st.FailedAt = now, time.Time{}
	if !notModified {
		st.ETag, st.Latest, st.URL = etag, latest, url
	}
	if err := c.save(st); err != nil {
		return c.result(st), fmt.Errorf("cannot save the update state: %w", err)
	}
	return c.result(st), nil
}

// ShouldNotify reports whether the player still has to be told about res (once per version).
// MarkNotified records it.
func (c *Checker) ShouldNotify(res Result) bool {
	if !res.Available {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	told := c.load().Notified
	return told == "" || Newer(res.Latest, told)
}

// MarkNotified remembers that version was announced, so it is not announced again.
func (c *Checker) MarkNotified(version string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.load()
	st.Notified = version
	return c.save(st)
}

func (c *Checker) result(st State) Result {
	r := Result{Current: c.Current, Latest: st.Latest}
	if st.Latest != "" && ValidDownloadURL(st.URL, st.Latest, c.Asset) {
		r.URL = st.URL
		r.Available = Newer(st.Latest, c.Current)
	}
	return r
}

type release struct {
	TagName string  `json:"tag_name"`
	Draft   bool    `json:"draft"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	// State is "uploaded" once the file is complete.
	State string `json:"state"`
}

// fetch asks GitHub for the releases and picks the newest one that has this platform's file.
func (c *Checker) fetch(ctx context.Context, etag string) (latest, link, newETag string, notModified bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	api := c.API
	if api == "" {
		api = ReleasesAPI
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return "", "", "", false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "vigil-agent/"+c.Current)
	if etag != "" {
		req.Header.Set("If-None-Match", etag) // a 304 does not count against GitHub's hourly limit
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: RequestTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return "", "", "", true, nil
	case http.StatusOK:
	default:
		return "", "", "", false, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var list []release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&list); err != nil {
		return "", "", "", false, fmt.Errorf("cannot read the release list: %w", err)
	}
	var best Version
	for _, r := range list {
		v, ok := Parse(r.TagName)
		if !ok || r.Draft {
			continue
		}
		for _, a := range r.Assets {
			if a.Name != c.Asset || (a.State != "" && a.State != "uploaded") || !ValidDownloadURL(a.URL, r.TagName, c.Asset) {
				continue
			}
			if latest == "" || Compare(v, best) > 0 {
				best, latest, link = v, r.TagName, a.URL
			}
		}
	}
	return latest, link, resp.Header.Get("ETag"), false, nil
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) load() State {
	if c.Load == nil {
		return State{}
	}
	return c.Load()
}

func (c *Checker) save(st State) error {
	if c.Save == nil {
		return nil
	}
	return c.Save(st)
}

// FileState keeps the State in a JSON file (written atomically). A missing or damaged file reads
// as an empty state: the next check simply asks GitHub again.
func FileState(path string) (load func() State, save func(State) error) {
	load = func() State {
		var st State
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &st)
		}
		return st
	}
	save = func(st State) error {
		data, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return err
		}
		return outbox.WriteFileAtomic(path, append(data, '\n'), 0o600)
	}
	return load, save
}
