package main

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/app"
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/notify"
	"github.com/vigil-tracker/agent/internal/outbox"
	"github.com/vigil-tracker/agent/internal/tray"
)

// notifier announces trophies in the style the user picked (Vigil popup, Windows notification
// or nothing). Safe for concurrent use.
type notifier struct {
	c *controller

	mu        sync.Mutex
	presenter *notify.Presenter
	builder   *notify.Builder
	server    string // the server the builder was made for
	token     string // current link credential; never logged or exposed in the UI
	windowsQ  notify.Queue
	windowsOn bool // a goroutine is showing Windows notifications
	// balloonURL is what a click on the last Windows notification opens: the download for an
	// update notice, "" for a trophy (then the click opens the profile, as before).
	balloonURL string
}

// update announces a newer version in the chosen style (nothing with notifications off: the menu
// entry still says it). url is the official download, already validated.
func (n *notifier) update(version, url string) {
	prefs := n.c.store.NotifyPrefs()
	if prefs.Style == config.StyleOff {
		n.c.log.Printf("update %s available (notifications are off; shown in the menu)", version)
		return
	}
	n.show(prefs, true, notify.UpdateItem(version, url))
}

// takeBalloonURL is the URL the last Windows notification stands for, if any.
func (n *notifier) takeBalloonURL() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.balloonURL
}

// builderFor returns the item builder for the current server and site.
func (n *notifier) builderFor(site string) *notify.Builder {
	server := n.c.store.Server()
	token, _ := n.c.store.Token()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.builder == nil || n.server != server || n.token != token {
		var games notify.GameSource
		if client, err := api.New(server, token); err == nil {
			games = client // GameInfo stays public; UnlockedTrophy sends the linked token.
		}
		n.builder = &notify.Builder{Games: games, Lang: n.c.language(),
			Icons: &notify.IconCache{Dir: filepath.Join(n.c.store.Dir, "icons"), Server: server}}
		n.server = server
		n.token = token
	}
	n.builder.SiteURL = site
	return n.builder
}

// popup starts the Vigil-style presenter on first use.
func (n *notifier) popup() *notify.Presenter {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.presenter == nil {
		p, err := notify.NewPresenter(n.c.language(), n.c.store.NotifyPrefs, func(url string) {
			if err := tray.OpenURL(url); err != nil {
				n.c.log.Printf("cannot open the game page: %v", err)
			}
		})
		if err != nil {
			n.c.log.Printf("notifications: %v", err)
			return nil
		}
		p.Log = n.c.log.Printf
		n.presenter = p
	}
	return n.presenter
}

// unlocks is the engine's OnUnlocks: trophies the server accepted while the app was open.
func (n *notifier) unlocks(events []outbox.Event) {
	prefs := n.c.store.NotifyPrefs()
	if prefs.Style == config.StyleOff || len(events) == 0 {
		return
	}
	n.c.mu.Lock()
	site := n.c.siteURL
	n.c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	items := n.builderFor(site).Items(ctx, events)
	n.show(prefs, false, items...)
}

// show sends cards to the chosen style. inOrder: never merge them (the test sequence).
func (n *notifier) show(prefs config.NotifyPrefs, inOrder bool, items ...notify.Item) {
	if prefs.Style == config.StyleWindows {
		if inOrder {
			n.windowsQ.AddInOrder(items...)
		} else {
			n.windowsQ.Add(items...)
		}
		n.mu.Lock()
		start := !n.windowsOn
		n.windowsOn = true
		n.mu.Unlock()
		if start {
			go n.windowsLoop()
		}
		return
	}
	p := n.popup()
	if p == nil {
		return
	}
	if inOrder {
		p.ShowInOrder(items...)
	} else {
		p.Show(items...)
	}
}

// windowsLoop shows queued Windows notifications a few seconds apart (so each one is seen).
func (n *notifier) windowsLoop() {
	for {
		it, ok := n.windowsQ.Next()
		if !ok {
			n.mu.Lock()
			n.windowsOn = false
			n.mu.Unlock()
			return
		}
		prefs := n.c.store.NotifyPrefs()
		lang := n.c.language()
		title, body := notify.T(lang, "windows_title", it.Trophy), notify.Subtitle(lang, it)
		switch {
		case it.Update:
			title, body = notify.Label(lang, it), notify.Title(lang, it)
		case it.Summary:
			title = notify.Title(lang, it)
		}
		n.mu.Lock()
		n.balloonURL = ""
		if it.Update {
			n.balloonURL = it.URL
		}
		n.mu.Unlock()
		// Windows' own sound is always off: the app plays its chime at the chosen volume.
		n.c.tray.NotifyIcon(title, body, it.Icon, true)
		if !it.Silent() {
			notify.PlaySound(it.IsPlatinum(), prefs.Volume)
		}
		time.Sleep(4 * time.Second)
	}
}

// test shows one sample trophy of each grade, in order. Nothing is sent to the server.
func (n *notifier) test() {
	prefs := n.c.store.NotifyPrefs()
	if prefs.Style == config.StyleOff {
		prefs.Style = config.StyleVigil // "off" still lets the user see what the cards look like
	}
	n.show(prefs, true, notify.Samples(n.c.language())...)
}

// command handles the "Notifications" submenu. It reports whether id was one of its entries.
func (n *notifier) command(id int) bool {
	prefs := n.c.store.NotifyPrefs()
	switch {
	case id == app.CmdNotifyVigil:
		prefs.Style = config.StyleVigil
	case id == app.CmdNotifyWindows:
		prefs.Style = config.StyleWindows
	case id == app.CmdNotifyOff:
		prefs.Style = config.StyleOff
	case id >= app.CmdVolumeBase && id < app.CmdVolumeBase+len(config.Volumes):
		prefs.Volume = config.Volumes[id-app.CmdVolumeBase]
	case id >= app.CmdCornerBase && id < app.CmdCornerBase+len(config.Corners):
		prefs.Corner = config.Corners[id-app.CmdCornerBase]
	case id == app.CmdNotifyTest:
		go n.test()
		return true
	default:
		return false
	}
	if err := n.c.store.SetNotifyPrefs(prefs); err != nil {
		n.c.log.Printf("cannot save the notification settings: %v", err)
	} else {
		n.c.log.Printf("notifications: style %s, volume %s, corner %s", prefs.Style, prefs.Normalized().Volume, prefs.Corner)
	}
	return true
}

// setLang switches the language of the cards and of the names asked from the server.
func (n *notifier) setLang(lang string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.builder != nil {
		n.builder.SetLang(lang)
	}
	if n.presenter != nil {
		n.presenter.SetLang(lang)
	}
}

// close stops the popup thread.
func (n *notifier) close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.presenter != nil {
		n.presenter.Close()
		n.presenter = nil
	}
}
