// Command vigil-agent-tray is the Windows app for players: an icon in the notification area that
// links the PC, finds the emulators and ports by itself and syncs in the background. It does the
// same as "vigil-agent run" (same config folder, token, queue and sources), so the command line
// and the tray can be used interchangeably (not at the same time).
//
// Built with -ldflags "-H windowsgui" so no console window opens. It opens no network port: the
// only UI is the menu, native dialogs, notifications and the website in the default browser.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/app"
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/i18n"
	"github.com/vigil-tracker/agent/internal/source/detect"
	"github.com/vigil-tracker/agent/internal/tray"
	"github.com/vigil-tracker/agent/internal/vui"
)

// mutexName keeps one copy per Windows user and settings folder: the normal install has one;
// a copy started with another VIGIL_CONFIG_DIR (tests, development) is a separate instance.
func mutexName() string {
	name := "VigilTrophiesTray"
	if dir := os.Getenv("VIGIL_CONFIG_DIR"); dir != "" {
		sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dir))))
		name += "-" + hex.EncodeToString(sum[:8])
	}
	return name
}

// maxLogSize: tray.log is rotated to tray.log.1 when it grows past this.
const maxLogSize = 1 << 20

func main() {
	release, first, err := app.SingleInstance(mutexName())
	if err == nil && !first {
		// Already running: show its menu instead of a second icon.
		if os.Getenv("VIGIL_CONFIG_DIR") != "" || !tray.AskSecondCopyToShowMenu() {
			t := app.Texts(i18n.Resolve(i18n.Auto, app.UserLocale()))
			vui.Show(vui.Dialog{Title: t.T("app"), Intro: t.T("already_running"), Yes: t.T("btn_ok")})
		}
		return
	}
	if err == nil {
		defer release()
	}
	store, err := config.Open()
	if err != nil {
		t := app.Texts(i18n.Resolve(i18n.Auto, app.UserLocale()))
		vui.Show(vui.Dialog{Title: t.T("app"), Intro: t.T("cannot_open_settings"), Yes: t.T("btn_ok")})
		os.Exit(1)
	}
	logFile := openLog(store)
	defer logFile.Close()
	c := newController(store, log.New(logFile, "", log.LstdFlags), filepath.Join(store.Dir, "tray.log"))
	if err := c.run(); err != nil {
		c.log.Printf("tray stopped: %v", err)
		os.Exit(1)
	}
}

// openLog appends to <config>/tray.log, keeping one older copy. The log never holds the token
// (nothing in the agent logs it).
func openLog(store *config.Store) io.WriteCloser {
	path := filepath.Join(store.Dir, "tray.log")
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogSize {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nopCloser{io.Discard}
	}
	return f
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// controller holds the tray's state; the menu reads it on the UI thread, goroutines update it.
type controller struct {
	store   *config.Store
	log     *log.Logger
	logPath string
	tray    *tray.Tray
	engine  *app.Engine
	notes   *notifier
	updates *updater
	auto    app.Autostart

	mu          sync.Mutex
	username    string
	profileURL  string
	siteURL     string
	linkingCode string
	cancelLink  context.CancelFunc
	summaries   []app.Summary
	// what the last menu showed, so a choice acts on the folder the player saw (UI thread)
	menuFolders []app.Folder
	menuStopped []config.StoppedFolder
	folderMu    sync.Mutex
	lastState   app.State

	langMu sync.RWMutex
	lang   string // resolved: en or pt-BR
}

// language is the app's current language.
func (c *controller) language() string {
	c.langMu.RLock()
	defer c.langMu.RUnlock()
	return c.lang
}

// t is the texts in the current language.
func (c *controller) t() app.Text { return app.Texts(c.language()) }

// applyLanguage resolves the saved choice (auto = Windows' language) and updates everything.
func (c *controller) applyLanguage() {
	lang := i18n.Resolve(c.store.Language(), app.UserLocale())
	c.langMu.Lock()
	c.lang = lang
	c.langMu.Unlock()
	c.notes.setLang(lang)
	c.updateTooltip()
}

func newController(store *config.Store, logger *log.Logger, logPath string) *controller {
	lang := i18n.Resolve(store.Language(), app.UserLocale())
	exe, _ := os.Executable()
	c := &controller{store: store, log: logger, logPath: logPath, lang: lang,
		auto: app.Autostart{Key: app.RegistryRunKey{Path: app.RunKeyPath}, Exe: exe}}
	c.notes = &notifier{c: c}
	c.updates = newUpdater(c)
	c.engine = &app.Engine{Store: store, Log: logger, OnUnlocks: c.notes.unlocks, OnChange: c.updateTooltip}
	c.tray = &tray.Tray{
		Tooltip:        app.Texts(lang).T("app"),
		Menu:           c.menu,
		NativeMenu:     store.SystemMenu,
		OnCommand:      c.command,
		OnBalloonClick: func() { go c.balloonClick() },
		OnSecondCopy:   func() {},
	}
	return c
}

func (c *controller) run() error {
	c.log.Printf("vigil-agent-tray %s starting (server %s)", api.Version, c.store.Server())
	go c.startup()
	err := c.tray.Run()
	c.engine.Close()
	c.notes.close()
	c.log.Printf("vigil-agent-tray stopped")
	return err
}

// startup: source detection (the first time, or once more after detection improved), account
// name, then the sync loop.
func (c *controller) startup() {
	go c.updates.loop()
	switch {
	case !c.store.TraySetupDone():
		c.offerDetected(detectFirstRun)
		if err := c.store.SetTraySetupDone(); err != nil {
			c.log.Printf("cannot save settings: %v", err)
		}
	case c.store.DetectVersion() < app.DetectVersion:
		c.offerDetected(detectUpdate)
	}
	if c.store.DetectVersion() < app.DetectVersion {
		if err := c.store.SetDetectVersion(app.DetectVersion); err != nil {
			c.log.Printf("cannot save settings: %v", err)
		}
	}
	c.engine.Restart()
	c.refreshAccount()
	c.refreshSummaries()
	if c.engine.Status().State == app.NotLinked {
		c.tray.Notify(c.t().T("app"), c.t().T("welcome"))
	}
	// Keep the source counts in the menu fresh.
	for range time.Tick(2 * time.Minute) {
		c.refreshSummaries()
	}
}

func (c *controller) menu() []app.Item {
	c.mu.Lock()
	m := app.MenuState{Username: c.username, LinkingCode: c.linkingCode, ProfileURL: c.profileURL,
		Summaries: append([]app.Summary(nil), c.summaries...)}
	c.mu.Unlock()
	m.Status = c.engine.Status()
	m.Autostart, _ = c.auto.Enabled()
	m.Notify = c.store.NotifyPrefs()
	m.Language = c.store.Language()
	m.Update, m.Checking = c.updates.available()
	m.Stopped = c.store.StoppedFolders()
	m.SystemMenu = c.store.SystemMenu()
	c.menuFolders, c.menuStopped = app.Folders(m.Summaries), m.Stopped
	return app.BuildMenu(c.t(), m)
}

// balloonClick: a Windows notification was clicked. An update notice opens the download, a trophy
// opens the profile.
func (c *controller) balloonClick() {
	if url := c.notes.takeBalloonURL(); url != "" {
		c.updates.openDownload()
		return
	}
	c.openProfile()
}

// command runs on the UI thread: everything slow goes to a goroutine.
func (c *controller) command(id int) {
	if id == app.CmdSystemMenu {
		if err := c.store.SetSystemMenu(!c.store.SystemMenu()); err != nil {
			c.log.Printf("cannot save menu preference: %v", err)
		}
		return
	}
	if c.notes.command(id) {
		return
	}
	if id >= app.CmdLangBase && id < app.CmdLangBase+len(app.LanguageChoices) {
		if err := c.store.SetLanguage(app.LanguageChoices[id-app.CmdLangBase]); err != nil {
			c.log.Printf("cannot save the language: %v", err)
		}
		c.applyLanguage()
		c.log.Printf("language: %s (%s)", c.store.Language(), c.language())
		return
	}
	switch {
	case id == app.CmdLink:
		go c.link()
	case id == app.CmdCancelLink:
		c.mu.Lock()
		if c.cancelLink != nil {
			c.cancelLink()
		}
		c.mu.Unlock()
	case id == app.CmdDetect:
		go c.offerDetected(detectManual)
	case id == app.CmdAddFolder:
		go c.addFolder()
	case id == app.CmdProfile:
		go c.openProfile()
	case id == app.CmdPause:
		paused := c.engine.Status().Paused
		go func() { c.engine.SetPaused(!paused) }()
	case id == app.CmdAutostart:
		on, _ := c.auto.Enabled()
		if err := c.auto.SetEnabled(!on); err != nil {
			c.log.Printf("start with Windows: %v", err)
		} else {
			c.log.Printf("start with Windows: %v", !on)
		}
	case id == app.CmdUpdate:
		go c.updates.openDownload()
	case id == app.CmdCheckUpdate:
		go c.updates.manual()
	case id == app.CmdLog:
		if err := tray.OpenPath(c.logPath); err != nil {
			c.log.Printf("cannot open the log: %v", err)
		}
	case id == app.CmdQuit:
		c.tray.Quit()
	case id >= app.CmdFolderRestoreBase:
		if i := id - app.CmdFolderRestoreBase; i < len(c.menuStopped) {
			go c.watchAgain(c.menuStopped[i])
		}
	case id >= app.CmdFolderStopBase:
		if i := id - app.CmdFolderStopBase; i < len(c.menuFolders) {
			go c.stopWatching(c.menuFolders[i])
		}
	case id >= app.CmdFolderOpenBase:
		if i := id - app.CmdFolderOpenBase; i < len(c.menuFolders) {
			if err := tray.OpenPath(c.menuFolders[i].Path); err != nil {
				c.log.Printf("cannot open the folder: %v", err)
			}
		}
	}
}

// ---- linking ----

func (c *controller) link() {
	c.mu.Lock()
	if c.linkingCode != "" {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancelLink = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		c.linkingCode, c.cancelLink = "", nil
		c.mu.Unlock()
		c.updateTooltip()
	}()

	client, err := api.New(c.store.Server(), "")
	if err != nil {
		c.linkFailed(err)
		return
	}
	auth, err := client.Authorize(ctx)
	if err != nil {
		c.linkFailed(err)
		return
	}
	host, _ := os.Hostname()
	page, err := app.LinkPage(auth, host)
	if err != nil {
		c.linkFailed(err)
		return
	}
	c.mu.Lock()
	c.linkingCode = auth.UserCode
	c.mu.Unlock()
	c.updateTooltip()
	c.log.Printf("linking: waiting for the code to be confirmed in the browser")
	if err := tray.OpenURL(page); err != nil {
		c.log.Printf("cannot open the browser: %v", err)
	}
	c.tray.Notify(c.t().T("app"), c.t().T("open_browser", auth.UserCode))

	token, err := app.WaitForToken(ctx, client, auth)
	if err != nil {
		if ctx.Err() == nil {
			c.linkFailed(err)
		}
		return
	}
	if err := c.store.SetServer(client.BaseURL); err != nil {
		c.linkFailed(err)
		return
	}
	if err := c.store.SetToken(token); err != nil {
		c.linkFailed(fmt.Errorf("cannot save the token: %w", err))
		return
	}
	c.log.Printf("linked to %s", client.BaseURL)
	c.engine.Restart()
	c.refreshAccount()
	c.mu.Lock()
	who := c.username
	c.mu.Unlock()
	if who == "" {
		who = "?"
	}
	c.tray.Notify(c.t().T("app"), c.t().T("linked_ok", who))
}

func (c *controller) linkFailed(err error) {
	c.log.Printf("linking failed: %v", err)
	t := c.t()
	c.tray.Notify(t.T("app"), t.T("link_failed", t.T(app.LinkErrorKey(err))))
}

// refreshAccount asks the server who this PC is linked to.
func (c *controller) refreshAccount() {
	token, err := c.store.Token()
	if err != nil || token == "" {
		c.setAccount("", "", "")
		return
	}
	client, err := api.New(c.store.Server(), token)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	me, err := client.Me(ctx)
	if err != nil {
		c.log.Printf("cannot ask the server who this PC belongs to: %v", err)
		return
	}
	profile, site := "", ""
	if _, err := app.SafeWebURL(me.ProfileURL); err == nil {
		profile = me.ProfileURL
	}
	if _, err := app.SafeWebURL(me.SiteURL); err == nil {
		site = me.SiteURL
	}
	c.log.Printf("linked as %s (device %q)", me.Username, me.DeviceName)
	c.setAccount(me.Username, profile, site)
}

func (c *controller) setAccount(username, profile, site string) {
	c.mu.Lock()
	c.username, c.profileURL, c.siteURL = username, profile, site
	c.mu.Unlock()
	c.updateTooltip()
}

func (c *controller) openProfile() {
	c.mu.Lock()
	profile := c.profileURL
	c.mu.Unlock()
	if profile == "" {
		c.refreshAccount()
		c.mu.Lock()
		profile = c.profileURL
		c.mu.Unlock()
	}
	if profile != "" {
		if err := tray.OpenURL(profile); err != nil {
			c.log.Printf("cannot open the profile: %v", err)
		}
	}
}

// ---- sources ----

// When offerDetected runs: the first time the app starts, once after detection improved (only
// when something changed, without a message otherwise), or from the menu.
type detectMode int

const (
	detectFirstRun detectMode = iota
	detectUpdate
	detectManual
)

// offerDetected looks in the usual places and asks before watching what it found. Folders are
// grouped (bbport's patch file and log are one line) and uncertain matches without trophies are
// left out. A configured source that detection now gives another type in the same folder (a
// bbport user folder saved as shadPS4 by an older version) is replaced if the user accepts.
func (c *controller) offerDetected(mode detectMode) {
	t := c.t()
	// folders the player stopped watching are never offered again (only "Watch again" brings them back)
	found := app.WithoutStopped(detect.Find(detect.FromOS(nil)), c.store.StoppedFolders())
	plan := app.PlanDetection(c.store.Sources(), found)
	offers := app.MarkReplacements(app.BuildOffers(plan.Add, app.SummarizeCandidate), plan.Replace)
	if len(offers) == 0 {
		switch mode {
		case detectFirstRun:
			c.tray.Notify(t.T("detect_title"), t.T("detect_none"))
		case detectManual:
			vui.Show(vui.Dialog{Title: t.T("detect_title"), Intro: t.T("detect_none"), Yes: t.T("btn_ok")})
		}
		return
	}
	intro := t.T("detect_intro")
	if mode == detectUpdate {
		intro = t.T("detect_update_intro")
	}
	d := vui.Dialog{Title: t.T("detect_title"), Intro: intro, Question: t.T("detect_question"),
		Yes: t.T("btn_watch"), No: t.T("btn_not_now")}
	for _, o := range offers {
		d.Lines = append(d.Lines, vui.Line{Title: o.Title(t), Detail: o.Path})
	}
	if extra := len(offers) - vui.MaxLines; extra > 0 {
		d.More = t.N("detect_more", extra)
	}
	if !vui.Show(d) {
		c.log.Printf("detected %d folder(s); the user declined", len(offers))
		return
	}
	added, removed, err := app.ApplyOffers(c.store, offers)
	if err != nil {
		c.log.Printf("cannot save sources: %v", err)
	}
	c.sourcesChanged(added, removed)
}

func (c *controller) addFolder() {
	dir, ok := tray.PickFolder(c.t().T("pick_folder"))
	if !ok {
		return
	}
	cands := detect.Classify(dir)
	if len(cands) == 0 {
		t := c.t()
		vui.Show(vui.Dialog{Title: t.T("app"), Intro: t.T("folder_unknown"), Yes: t.T("btn_ok")})
		return
	}
	c.addSources(cands)
}

func (c *controller) addSources(cands []detect.Candidate) {
	n, err := app.AddCandidates(c.store, cands)
	if err != nil {
		c.log.Printf("cannot save sources: %v", err)
	}
	c.sourcesChanged(n, 0)
}

func (c *controller) sourcesChanged(added, removed int) {
	c.log.Printf("%d new source(s) added", added)
	if removed > 0 {
		c.log.Printf("%d source(s) of the wrong type removed", removed)
	}
	c.engine.Restart()
	c.refreshSummaries()
	c.tray.Notify(c.t().T("detect_title"), c.t().T("detect_added", len(c.store.Sources())))
}

// stopWatching asks first, then removes every source of the folder from the configuration and
// remembers the folder as stopped. Only the configuration changes: the folder's files, the trophies
// already sent, the queue and the link stay as they are; the running sync restarts without it.
func (c *controller) stopWatching(f app.Folder) {
	c.folderMu.Lock()
	defer c.folderMu.Unlock()
	t := c.t()
	if !vui.Show(vui.Dialog{Title: t.T("stop_title"), Intro: t.T("stop_text"),
		Lines:    []vui.Line{{Title: f.Types(t), Detail: f.Path}},
		Question: t.T("stop_more"), Yes: t.T("btn_stop"), No: t.T("btn_cancel")}) {
		return
	}
	n, err := c.store.StopWatching(f.Path, time.Now())
	if err != nil {
		c.log.Printf("cannot save settings: %v", err)
		return
	}
	c.log.Printf("stopped watching a folder (%d source(s)); it will not be offered again", n)
	c.engine.Restart()
	c.refreshSummaries()
}

// watchAgain is the explicit way back for a stopped folder: what was configured there returns.
func (c *controller) watchAgain(f config.StoppedFolder) {
	c.folderMu.Lock()
	defer c.folderMu.Unlock()
	n, err := c.store.WatchAgain(f.Path)
	if err != nil {
		c.log.Printf("cannot save settings: %v", err)
		return
	}
	c.log.Printf("watching a stopped folder again (%d source(s))", n)
	c.engine.Restart()
	c.refreshSummaries()
	c.tray.Notify(c.t().T("app"), c.t().T("folder_watching_again", f.Path))
}

func (c *controller) refreshSummaries() {
	var sums []app.Summary
	for _, s := range app.Dedupe(c.store.Sources()) {
		sums = append(sums, app.Summarize(context.Background(), s))
	}
	c.mu.Lock()
	c.summaries = sums
	c.mu.Unlock()
}

func (c *controller) updateTooltip() {
	c.mu.Lock()
	m := app.MenuState{Username: c.username, LinkingCode: c.linkingCode}
	c.mu.Unlock()
	m.Status = c.engine.Status()
	c.mu.Lock()
	becameUnauthorized := m.Status.State == app.Unauthorized && c.lastState != app.Unauthorized
	c.lastState = m.Status.State
	c.mu.Unlock()
	t := c.t()
	tip := t.T("app") + " — " + app.StatusLine(t, m)
	if line := app.SyncLine(t, m.Status); line != "" && m.Status.State != app.NotLinked {
		tip += " · " + line
	}
	if becameUnauthorized {
		c.tray.Notify(c.t().T("app"), c.t().T("unauthorized"))
	}
	c.tray.SetTooltip(tip)
}
