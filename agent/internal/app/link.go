package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
)

// LinkPage is the page where the user confirms the code: the server's verification_uri_complete
// (or verification_uri) with ?name= added. It must be http(s); plain http only for a local
// server, like the API itself, so the browser is never sent anywhere odd.
func LinkPage(auth *api.Authorization, deviceName string) (string, error) {
	link := auth.VerificationURIComplete
	if link == "" {
		link = auth.VerificationURI
	}
	u, err := SafeWebURL(link)
	if err != nil {
		return "", fmt.Errorf("the server sent an invalid link page: %w", err)
	}
	if deviceName != "" {
		q := u.Query()
		q.Set("name", deviceName)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// SafeWebURL accepts only https URLs, or http to a loopback host (development).
func SafeWebURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return nil, errors.New("not a web address")
	}
	switch u.Scheme {
	case "https":
		return u, nil
	case "http":
		if _, err := api.NormalizeServerURL(u.Scheme + "://" + u.Host); err == nil {
			return u, nil
		}
	}
	return nil, errors.New("only https addresses are opened")
}

// ErrLinkDenied is returned when the user pressed "deny" on the website.
var ErrLinkDenied = errors.New("the request was denied on the website")

// ErrLinkExpired is returned when the code expired (or was already used): start again.
var ErrLinkExpired = errors.New("the code expired; run link again")

// LinkErrorKey is the text key (internal/i18n) that explains a linking error to the user.
func LinkErrorKey(err error) string {
	var retry *api.RetryableError
	switch {
	case errors.Is(err, ErrLinkDenied):
		return "err_denied"
	case errors.Is(err, ErrLinkExpired):
		return "err_expired"
	case errors.As(err, &retry):
		return "err_network"
	}
	return "err_unexpected"
}

// WaitForToken polls every interval seconds, slowing down when the server says so (RFC 8628 §3.5).
func WaitForToken(ctx context.Context, client *api.Client, auth *api.Authorization) (string, error) {
	interval := time.Duration(max(auth.Interval, 1)) * time.Second
	deadline := time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return "", errors.New("cancelled")
		case <-time.After(interval):
		}
		token, err := client.PollToken(ctx, auth.DeviceCode)
		var poll *api.PollError
		var retry *api.RetryableError
		switch {
		case err == nil:
			return token, nil
		case errors.As(err, &poll) && poll.Code == "authorization_pending":
		case errors.As(err, &poll) && poll.Code == "slow_down":
			interval += 5 * time.Second
		case errors.As(err, &poll) && poll.Code == "access_denied":
			return "", ErrLinkDenied
		case errors.As(err, &poll):
			return "", fmt.Errorf("%w (%s)", ErrLinkExpired, poll.Code)
		case errors.As(err, &retry):
			interval = max(interval, retry.RetryAfter) // server busy: keep trying until the code expires
		default:
			return "", err
		}
		if time.Now().After(deadline) {
			return "", ErrLinkExpired
		}
	}
}
