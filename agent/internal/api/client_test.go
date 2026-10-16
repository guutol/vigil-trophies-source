package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vigil-tracker/agent/internal/outbox"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "vgl_secret")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var batch = []outbox.Event{{EventID: "e1", TitleID: "CUSA03173", TrophyID: 1, UnlockedAt: "2026-10-10T06:30:00Z"}}

func TestSendUnlocksSuccess(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/unlocks" || r.Header.Get("Authorization") != "Bearer vgl_secret" {
			t.Errorf("bad request: %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			Events []map[string]any `json:"events"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Events) != 1 || body.Events[0]["event_id"] != "e1" || body.Events[0]["source_end"] != nil {
			t.Errorf("bad body: %+v", body)
		}
		w.Write([]byte(`{"accepted":1,"duplicates":0,"rejected":[]}`))
	})
	res, err := c.SendUnlocks(context.Background(), batch)
	if err != nil || res.Accepted != 1 {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestSendUnlocks429RetryAfter(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := c.SendUnlocks(context.Background(), batch)
	var retry *RetryableError
	if !errors.As(err, &retry) || retry.Status != 429 || retry.RetryAfter != 42*time.Second {
		t.Fatalf("want retryable 429 with 42s, got %v", err)
	}
}

func TestSendUnlocks500IsRetryable(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.SendUnlocks(context.Background(), batch)
	var retry *RetryableError
	if !errors.As(err, &retry) || retry.Status != 500 || retry.RetryAfter != 0 {
		t.Fatalf("want retryable 500, got %v", err)
	}
}

func TestSendUnlocks401(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, err := c.SendUnlocks(context.Background(), batch); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestNetworkErrorIsRetryableAndHidesToken(t *testing.T) {
	c, _ := New("http://127.0.0.1:1", "vgl_secret")
	_, err := c.SendUnlocks(context.Background(), batch)
	var retry *RetryableError
	if !errors.As(err, &retry) {
		t.Fatalf("want retryable, got %v", err)
	}
	if strings.Contains(err.Error(), "vgl_secret") {
		t.Fatal("error message leaks the token")
	}
}

func TestPollTokenErrors(t *testing.T) {
	codes := []string{"authorization_pending", "slow_down", "access_denied", "expired_token"}
	for _, code := range codes {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"` + code + `","message":"x"}`))
		})
		_, err := c.PollToken(context.Background(), "dc")
		var poll *PollError
		if !errors.As(err, &poll) || poll.Code != code {
			t.Errorf("%s: got %v", code, err)
		}
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"vgl_new","token_type":"Bearer"}`))
	})
	if tok, err := c.PollToken(context.Background(), "dc"); err != nil || tok != "vgl_new" {
		t.Fatalf("got %q %v", tok, err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)
	if d := ParseRetryAfter("5", now); d != 5*time.Second {
		t.Errorf("seconds: %v", d)
	}
	if d := ParseRetryAfter("Sat, 10 Oct 2026 06:01:00 GMT", now); d != time.Minute {
		t.Errorf("date: %v", d)
	}
	for _, v := range []string{"", "-3", "soon", "Sat, 10 Oct 2026 05:00:00 GMT"} {
		if d := ParseRetryAfter(v, now); d != 0 {
			t.Errorf("%q: %v", v, d)
		}
	}
}

func TestServerURLRules(t *testing.T) {
	ok := map[string]string{
		"https://vigil.example/":  "https://vigil.example",
		"http://localhost:8080":   "http://localhost:8080",
		"http://127.0.0.1:8080/":  "http://127.0.0.1:8080",
		"https://x.example/api?q": "https://x.example/api",
	}
	for in, want := range ok {
		if got, err := NormalizeServerURL(in); err != nil || got != want {
			t.Errorf("%s: got %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"http://vigil.example", "ftp://x", "vigil.example", ""} {
		if _, err := NormalizeServerURL(bad); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestMeSendsTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/me" || r.Header.Get("Authorization") != "Bearer vgl_me" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"username": "guto", "device_name": "PC", "profile_url": "http://localhost:5173/u/guto"})
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "vgl_me")
	me, err := c.Me(context.Background())
	if err != nil || me.Username != "guto" || me.ProfileURL != "http://localhost:5173/u/guto" {
		t.Fatalf("me=%+v err=%v", me, err)
	}
	c.Token = "vgl_other"
	if _, err := c.Me(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked token: %v", err)
	}
}

func TestGameInfoFollowsTheGameRedirect(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/games/NPWR05818_00":
			http.Redirect(w, r, "/api/games/bloodborne?set=NPWR05818_00&lang="+r.URL.Query().Get("lang"), http.StatusPermanentRedirect)
		case "/api/games/bloodborne":
			if r.URL.Query().Get("lang") != "pt-BR" {
				t.Errorf("lang lost: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"slug":"bloodborne","name":"Bloodborne","selected":{"trophy_set_id":"NPWR05818_00","trophies":[{"id":0,"name":"Platina","grade":"platinum","icon_url":"/api/catalog/NPWR05818_00/icons/0.png"},{"id":5,"name":"Caçador","grade":"gold"},{"id":9,"hidden":true}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "vgl_secret")
	info, err := c.GameInfo(context.Background(), "NPWR05818_00", "pt-BR")
	if err != nil || info.Slug != "bloodborne" || info.Name != "Bloodborne" || info.SetID != "NPWR05818_00" ||
		info.Trophies[5].Name != "Caçador" || info.Trophies[5].Grade != "gold" ||
		info.Trophies[0].IconURL != "/api/catalog/NPWR05818_00/icons/0.png" || !info.Trophies[9].Hidden || len(info.Trophies) != 3 {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	for _, a := range auth {
		if a != "" {
			t.Fatal("a public page must not get the token")
		}
	}
	var rej *RejectedError
	if _, err := c.GameInfo(context.Background(), "NPWR99999_00", "en"); !errors.As(err, &rej) || rej.Status != 404 {
		t.Fatalf("unknown set: %v", err)
	}
}
