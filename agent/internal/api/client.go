// Package api talks to the Vigil server: device flow and unlock ingestion.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vigil-tracker/agent/internal/outbox"
)

// Version is sent in the User-Agent header. Release builds set it with
// -ldflags "-X github.com/vigil-tracker/agent/internal/api.Version=v1.2.3".
var Version = "dev"

// Timeout bounds every HTTP request.
const Timeout = 10 * time.Second

// Client calls one server. The token is only ever placed in the Authorization header.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New validates baseURL and returns a client. Plain http is only accepted for loopback hosts,
// so the token never travels unencrypted over a network.
func New(baseURL, token string) (*Client, error) {
	base, err := NormalizeServerURL(baseURL)
	if err != nil {
		return nil, err
	}
	return &Client{BaseURL: base, Token: token, HTTP: &http.Client{Timeout: Timeout}}, nil
}

// NormalizeServerURL checks the scheme/host and strips a trailing slash.
func NormalizeServerURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("invalid server URL (expected https://host)")
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return "", fmt.Errorf("refusing plain http for a non-local server; use https")
	}
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ---- errors ----

// ErrUnauthorized means the token is missing, unknown or revoked: run "link" again.
var ErrUnauthorized = errors.New("unauthorized")

// RetryableError is a temporary failure (network, 5xx, 429). RetryAfter is zero when the
// server gave no hint.
type RetryableError struct {
	Status     int
	RetryAfter time.Duration
	Err        error
}

func (e *RetryableError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("server returned %d", e.Status)
	}
	return "network error: " + e.Err.Error()
}

func (e *RetryableError) Unwrap() error { return e.Err }

// RejectedError is a 4xx the agent cannot fix by retrying the same request.
type RejectedError struct {
	Status int
	Code   string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("server rejected the request (%d %s)", e.Status, e.Code)
}

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// ---- device flow ----

// Authorization is the answer to POST /api/device/authorize.
type Authorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// Authorize starts the device flow.
func (c *Client) Authorize(ctx context.Context) (*Authorization, error) {
	var out Authorization
	if _, err := c.do(ctx, http.MethodPost, "/api/device/authorize", nil, false, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PollError is a device-flow answer other than success: authorization_pending, slow_down,
// expired_token, access_denied or invalid_grant.
type PollError struct{ Code string }

func (e *PollError) Error() string { return e.Code }

// PollToken asks once for the token. It returns *PollError while the user has not approved.
func (c *Client) PollToken(ctx context.Context, deviceCode string) (string, error) {
	var out struct {
		AccessToken string `json:"access_token"`
	}
	_, err := c.do(ctx, http.MethodPost, "/api/device/token", map[string]string{"device_code": deviceCode}, false, &out)
	var rej *RejectedError
	if errors.As(err, &rej) && rej.Status == http.StatusBadRequest && rej.Code != "" {
		return "", &PollError{Code: rej.Code}
	}
	if err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", errors.New("server sent an empty token")
	}
	return out.AccessToken, nil
}

// ---- ingestion ----

// Rejected is an event the server refused; it will never be accepted, so it leaves the outbox.
type Rejected struct {
	EventID string `json:"event_id"`
	Reason  string `json:"reason"`
}

// SendResult is the answer to POST /api/agent/unlocks.
// Pending counts events stored for a trophy set the server's catalog does not know yet.
type SendResult struct {
	Accepted   int        `json:"accepted"`
	Duplicates int        `json:"duplicates"`
	Pending    int        `json:"pending"`
	Rejected   []Rejected `json:"rejected"`
}

// SendUnlocks posts up to 100 events.
func (c *Client) SendUnlocks(ctx context.Context, events []outbox.Event) (*SendResult, error) {
	body := struct {
		Events []outbox.Event `json:"events"`
	}{Events: events}
	var out SendResult
	if _, err := c.do(ctx, http.MethodPost, "/api/agent/unlocks", body, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- community catalog ----

// UploadTimeout bounds a catalog proposal upload (it carries images).
const UploadTimeout = 2 * time.Minute

// CatalogStatus says which trophy sets the server knows and which this user already proposed.
type CatalogStatus struct {
	Known    []string `json:"known"`
	Proposed []string `json:"proposed"`
}

// CatalogStatus asks about up to 100 set ids.
func (c *Client) CatalogStatus(ctx context.Context, setIDs []string) (*CatalogStatus, error) {
	var out CatalogStatus
	q := url.Values{"sets": {strings.Join(setIDs, ",")}}
	if _, err := c.do(ctx, http.MethodGet, "/api/agent/catalog-status?"+q.Encode(), nil, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProposeCatalog sends a catalog proposal; the answer is "pending", "published" or "known".
func (c *Client) ProposeCatalog(ctx context.Context, proposal any) (string, error) {
	var out struct {
		Status string `json:"status"`
	}
	upload := &http.Client{Timeout: UploadTimeout, Transport: c.HTTP.Transport}
	if _, err := c.doWith(ctx, upload, http.MethodPost, "/api/agent/catalog-proposals", proposal, true, &out); err != nil {
		return "", err
	}
	return out.Status, nil
}

// ---- account and trophy names (tray app) ----

// Me is who this agent is linked to (GET /api/agent/me).
type Me struct {
	Username   string `json:"username"`
	DeviceName string `json:"device_name"`
	ProfileURL string `json:"profile_url"`
	SiteURL    string `json:"site_url"`
}

// Me asks the server which account and device this token belongs to.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var out Me
	if _, err := c.do(ctx, http.MethodGet, "/api/agent/me", nil, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TrophyInfo is one trophy as the public game page shows it. Hidden trophies come without name and icon.
type TrophyInfo struct {
	Name    string `json:"name"`
	Grade   string `json:"grade"`
	IconURL string `json:"icon_url"`
	Hidden  bool   `json:"hidden"`
}

// GameInfo is what the notifications need from a game: its name, slug (for the game page) and trophies.
type GameInfo struct {
	Slug     string
	Name     string
	SetID    string
	Trophies map[int]TrophyInfo
}

// GameInfo reads the public game page of a trophy set (or title id) in lang; the server redirects a
// set id to its game with that set selected. An unknown set gives a *RejectedError with status 404.
// No token is sent: the page is public.
func (c *Client) GameInfo(ctx context.Context, setOrTitleID, lang string) (*GameInfo, error) {
	var out struct {
		Slug     string `json:"slug"`
		Name     string `json:"name"`
		Selected struct {
			TrophySetID string `json:"trophy_set_id"`
			Trophies    []struct {
				ID int `json:"id"`
				TrophyInfo
			} `json:"trophies"`
		} `json:"selected"`
	}
	path := "/api/games/" + url.PathEscape(setOrTitleID) + "?" + url.Values{"lang": {lang}}.Encode()
	if _, err := c.do(ctx, http.MethodGet, path, nil, false, &out); err != nil {
		return nil, err
	}
	info := &GameInfo{Slug: out.Slug, Name: out.Name, SetID: out.Selected.TrophySetID,
		Trophies: make(map[int]TrophyInfo, len(out.Selected.Trophies))}
	for _, t := range out.Selected.Trophies {
		info.Trophies[t.ID] = t.TrophyInfo
	}
	return info, nil
}

// ---- plumbing ----

func (c *Client) do(ctx context.Context, method, path string, in any, auth bool, out any) (int, error) {
	return c.doWith(ctx, c.HTTP, method, path, in, auth, out)
}

func (c *Client) doWith(ctx context.Context, hc *http.Client, method, path string, in any, auth bool, out any) (int, error) {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "vigil-agent/"+Version)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, &RetryableError{Err: scrub(err)}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return resp.StatusCode, fmt.Errorf("invalid response from server: %w", err)
			}
		}
		return resp.StatusCode, nil
	case resp.StatusCode == http.StatusUnauthorized && auth:
		return resp.StatusCode, ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return resp.StatusCode, &RetryableError{Status: resp.StatusCode,
			RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	default:
		var e apiError
		_ = json.Unmarshal(data, &e)
		return resp.StatusCode, &RejectedError{Status: resp.StatusCode, Code: e.Error}
	}
}

// ParseRetryAfter accepts delta-seconds or an HTTP date. Unparseable or past values give 0.
func ParseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// scrub drops the URL from transport errors (it is harmless, but keeps logs short and generic).
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
