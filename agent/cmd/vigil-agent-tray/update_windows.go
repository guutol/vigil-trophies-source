package main

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/tray"
	"github.com/vigil-tracker/agent/internal/update"
	"github.com/vigil-tracker/agent/internal/vui"
)

// When the app looks for a newer version: a while after it starts (never in the way of startup,
// source detection or the first sync), then every updateTick. The checker itself answers from its
// saved state for update.CheckEvery, so GitHub is asked at most every six hours.
const (
	updateFirstDelay = 45 * time.Second
	updateTick       = 30 * time.Minute
)

// updater tells the player that a newer Vigil is out: one notification per version (remembered in
// update.json), an "Update available" menu entry that stays, and a manual check. It never downloads,
// replaces or runs anything: the official download opens in the browser when the player clicks.
type updater struct {
	c     *controller
	check *update.Checker

	mu       sync.Mutex
	latest   update.Result
	checking bool
}

func newUpdater(c *controller) *updater {
	load, save := update.FileState(c.store.UpdatePath())
	return &updater{c: c, check: &update.Checker{
		Current: api.Version,
		Asset:   update.AssetFor(runtime.GOOS, runtime.GOARCH, true),
		Load:    load,
		Save:    save,
	}}
}

// loop runs for the life of the app. Errors (offline, GitHub limits) only go to the log.
func (u *updater) loop() {
	if _, ok := update.Parse(api.Version); !ok {
		u.c.log.Printf("update check off: development build %q", api.Version)
		return
	}
	time.Sleep(updateFirstDelay)
	for {
		u.run(false)
		time.Sleep(updateTick)
	}
}

// run checks (from the saved answer when it is recent, unless force) and announces a new version
// the player was not told about yet.
func (u *updater) run(force bool) (update.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), update.RequestTimeout+5*time.Second)
	defer cancel()
	res, err := u.check.Check(ctx, force)
	if err != nil {
		u.c.log.Printf("update check: %v", err)
	} else if !res.Cached {
		u.c.log.Printf("update check: newest %q (running %s)", res.Latest, res.Current)
	}
	u.mu.Lock()
	u.latest = res
	u.mu.Unlock()
	if u.check.ShouldNotify(res) {
		// A manual check answers in its own dialog; the background check shows the notification.
		if !force {
			u.c.notes.update(res.Latest, res.URL)
		}
		if err := u.check.MarkNotified(res.Latest); err != nil {
			u.c.log.Printf("cannot save the update state: %v", err)
		}
	}
	return res, err
}

// available is the newer version for the menu ("" = none known).
func (u *updater) available() (version string, checking bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.latest.Available {
		version = u.latest.Latest
	}
	return version, u.checking
}

// openDownload opens the official download of the newer version in the browser.
func (u *updater) openDownload() {
	u.mu.Lock()
	res := u.latest
	u.mu.Unlock()
	u.open(res)
}

func (u *updater) open(res update.Result) {
	if !res.Available || !update.ValidDownloadURL(res.URL, res.Latest, u.check.Asset) {
		return
	}
	if err := tray.OpenURL(res.URL); err != nil {
		u.c.log.Printf("cannot open the download: %v", err)
	}
}

// manual is "Check for updates" in the menu: always asks GitHub and says what it found.
func (u *updater) manual() {
	t := u.c.t()
	if _, ok := update.Parse(api.Version); !ok {
		vui.Show(vui.Dialog{Title: t.T("update_dialog_title"), Intro: t.T("update_dev", api.Version), Yes: t.T("btn_ok")})
		return
	}
	u.mu.Lock()
	if u.checking {
		u.mu.Unlock()
		return
	}
	u.checking = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.checking = false
		u.mu.Unlock()
	}()
	res, err := u.run(true)
	t = u.c.t()
	switch {
	case res.Available:
		if vui.Show(vui.Dialog{Title: t.T("update_dialog_title"), Intro: t.T("update_available", res.Latest, res.Current),
			Yes: t.T("btn_download"), No: t.T("btn_not_now")}) {
			u.open(res)
		}
	case err != nil:
		vui.Show(vui.Dialog{Title: t.T("update_dialog_title"), Intro: t.T("update_failed"), Yes: t.T("btn_ok")})
	default:
		vui.Show(vui.Dialog{Title: t.T("update_dialog_title"), Intro: t.T("update_none", res.Current), Yes: t.T("btn_ok")})
	}
}
