package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vigil-tracker/agent/internal/outbox"
)

// Icon cache limits.
const (
	MaxIconBytes  = 512 << 10 // one icon
	MaxIconPixels = 1024      // width and height
	MaxCacheBytes = 20 << 20  // the whole cache folder; the oldest icons go first
)

// IconCache downloads trophy icons from the configured server only, keeps the valid PNGs in a
// local folder and never grows past MaxCacheBytes.
type IconCache struct {
	Dir string
	// Server is the configured API address (https://..., or http://localhost in development).
	Server string
	HTTP   *http.Client

	mu sync.Mutex
}

// ErrForeignURL: the icon address is not on the configured server.
var ErrForeignURL = errors.New("icon address is not on the configured server")

// Resolve turns the server's icon address (usually "/api/catalog/...") into an absolute URL and
// checks that it stays on the configured server: same scheme, host and port, no credentials.
func (c *IconCache) Resolve(raw string) (*url.URL, error) {
	base, err := url.Parse(c.Server)
	if err != nil || base.Host == "" {
		return nil, errors.New("invalid server address")
	}
	ref, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || raw == "" {
		return nil, errors.New("invalid icon address")
	}
	u := base.ResolveReference(ref)
	if u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) || u.User != nil {
		return nil, ErrForeignURL
	}
	u.Fragment = ""
	return u, nil
}

// ValidatePNG accepts only a complete, decodable PNG of a sensible size.
func ValidatePNG(data []byte) error {
	if len(data) == 0 || len(data) > MaxIconBytes {
		return fmt.Errorf("icon size %d bytes is out of range", len(data))
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("not a PNG: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxIconPixels || cfg.Height > MaxIconPixels {
		return fmt.Errorf("icon is %dx%d", cfg.Width, cfg.Height)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("broken PNG: %w", err)
	}
	return nil
}

// Get returns the icon at raw (relative to the server), from the cache or downloaded.
func (c *IconCache) Get(ctx context.Context, raw string) ([]byte, error) {
	u, err := c.Resolve(raw)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(u.String()))
	path := filepath.Join(c.Dir, hex.EncodeToString(sum[:16])+".png")

	c.mu.Lock()
	defer c.mu.Unlock()
	if data, err := os.ReadFile(path); err == nil {
		if ValidatePNG(data) == nil {
			now := time.Now()
			_ = os.Chtimes(path, now, now) // recently used: evicted last
			return data, nil
		}
		_ = os.Remove(path)
	}
	data, err := c.download(ctx, u)
	if err != nil {
		return nil, err
	}
	if err := ValidatePNG(data); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(c.Dir, 0o700); err == nil {
		if err := outbox.WriteFileAtomic(path, data, 0o600); err == nil {
			c.trim()
		}
	}
	return data, nil
}

func (c *IconCache) download(ctx context.Context, u *url.URL) ([]byte, error) {
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	// Redirects must stay on the server too.
	guarded := *client
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if _, err := c.Resolve(req.URL.String()); err != nil {
			return err
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/png")
	resp, err := guarded.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("icon: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxIconBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxIconBytes {
		return nil, errors.New("icon too large")
	}
	return data, nil
}

// trim deletes the least recently used icons until the folder fits MaxCacheBytes.
func (c *IconCache) trim() {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		size int64
		mod  time.Time
	}
	var files []file
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		files = append(files, file{filepath.Join(c.Dir, e.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= MaxCacheBytes {
			return
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}
