package app

import (
	"errors"
	"strings"

	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/source/detect"
)

// Menu commands. Folder entries use a base + their index in Folders (or in the stopped list).
const (
	CmdLink = iota + 1
	CmdCancelLink
	CmdDetect
	CmdAddFolder
	CmdProfile
	CmdPause
	CmdAutostart
	CmdLog
	CmdQuit
	CmdNotifyVigil
	CmdNotifyWindows
	CmdNotifyOff
	CmdNotifyTest
	CmdUpdate      // open the official download of the newer version
	CmdCheckUpdate // ask GitHub now
	CmdSystemMenu
	CmdCornerBase        = 900  // + index in config.Corners
	CmdLangBase          = 950  // + index in LanguageChoices
	CmdVolumeBase        = 960  // + index in config.Volumes
	CmdFolderOpenBase    = 1000 // + index in Folders(Summaries): open the folder
	CmdFolderStopBase    = 2000 // + index in Folders(Summaries): stop watching it
	CmdFolderRestoreBase = 3000 // + index in MenuState.Stopped: watch it again
)

// Item is one tray menu entry. ID 0 with an empty label is a separator.
type Item struct {
	ID       int
	Label    string
	Disabled bool
	Checked  bool
	Default  bool // shown in bold (what a double click would do)
	Sub      []Item

	// For the Vigil menu (the native menu folds them into the label, see Text):
	Icon   string // icon name (internal/tray/icons), "" for none
	Detail string // a second line, e.g. a folder path
	Note   string // a third, quieter line, e.g. how the folder is watched
	Status bool   // an information line, not an action
	Danger bool   // an action that removes something (asks first)
}

// Text is the one-line label of the native menu: the label, the note and the detail.
func (it Item) Text() string {
	out := it.Label
	if it.Note != "" {
		out += " — " + it.Note
	}
	if it.Detail != "" {
		out += "  ·  " + it.Detail
	}
	return out
}

// AccessibleName is what a screen reader says for the item: every visible line, and the state.
func (it Item) AccessibleName(t Text) string {
	parts := []string{it.Label}
	if it.Note != "" {
		parts = append(parts, it.Note)
	}
	if it.Detail != "" {
		parts = append(parts, it.Detail)
	}
	if it.Checked {
		parts = append(parts, t.T("a11y_selected"))
	}
	if len(it.Sub) > 0 {
		parts = append(parts, t.T("a11y_submenu"))
	}
	return strings.Join(parts, ", ")
}

// Separator is a menu separator.
var Separator = Item{}

// MenuState is everything the menu shows.
type MenuState struct {
	Status      Status
	Username    string // "" until the server answered /api/agent/me
	LinkingCode string // the code being confirmed in the browser, while linking
	ProfileURL  string
	Summaries   []Summary
	Autostart   bool
	Notify      config.NotifyPrefs
	Language    string                 // saved choice: auto, en or pt-BR
	Stopped     []config.StoppedFolder // folders the player stopped watching
	Update      string                 // a newer version with an official download ("" = none known)
	Checking    bool                   // a manual update check is running
	SystemMenu  bool
}

// LanguageChoices are the entries of the Language submenu.
var LanguageChoices = []string{"auto", "en", "pt-BR"}

// StatusLine is the first (disabled) menu line and the icon tooltip.
func StatusLine(t Text, m MenuState) string {
	switch {
	case m.LinkingCode != "":
		return t.T("linking", m.LinkingCode)
	case m.Status.State == NotLinked:
		return t.T("not_linked")
	case m.Status.State == Unauthorized:
		return t.T("token_refused")
	case m.Username != "":
		return t.T("linked_as", m.Username)
	}
	return t.T("linked")
}

// SyncLine says what the sync is doing (second disabled line).
func SyncLine(t Text, st Status) string {
	switch {
	case st.Paused:
		return t.T("state_paused")
	case st.State == Running && st.Offline:
		return t.T("state_offline", st.Pending)
	case st.State == Running:
		return t.T("state_running")
	case st.State == NoSources:
		return t.T("state_no_sources")
	case st.State == Failed && st.Err != nil:
		msg := st.Err.Error()
		if len(msg) > 80 {
			msg = msg[:77] + "..."
		}
		return t.T("state_failed", msg)
	}
	return ""
}

// BuildMenu lays out the tray menu in groups: what the app is doing; a newer version; the account;
// the watched folders; syncing; preferences; updates, log and quit.
func BuildMenu(t Text, m MenuState) []Item {
	linked := m.Status.State != NotLinked && m.Status.State != Unauthorized
	items := []Item{{Label: StatusLine(t, m), Disabled: true, Status: true, Icon: statusIcon(m, linked)}}
	if line := SyncLine(t, m.Status); line != "" && linked && m.LinkingCode == "" {
		items = append(items, Item{Label: line, Disabled: true, Status: true})
	}
	items = append(items, Separator)
	// A newer version stays visible here: the notification can be missed.
	if m.Update != "" {
		items = append(items, Item{ID: CmdUpdate, Label: t.T("update_menu", m.Update), Icon: "update"}, Separator)
	}

	// account
	switch {
	case m.LinkingCode != "":
		items = append(items, Item{ID: CmdCancelLink, Label: t.T("cancel_link"), Icon: "link"})
	case !linked:
		label := t.T("link")
		if m.Status.State == Unauthorized {
			label = t.T("relink")
		}
		items = append(items, Item{ID: CmdLink, Label: label, Default: true, Icon: "link"})
	}
	items = append(items, Item{ID: CmdProfile, Label: t.T("profile"), Disabled: !linked || m.ProfileURL == "",
		Default: linked && m.LinkingCode == "", Icon: "profile"})

	// folders and syncing
	items = append(items, Separator, foldersMenu(t, m))
	pause := Item{ID: CmdPause, Label: t.T("pause"), Disabled: !linked, Icon: "pause"}
	if m.Status.Paused {
		pause.Label, pause.Icon = t.T("resume"), "play"
	}
	items = append(items, pause)

	// preferences
	items = append(items, Separator, notifyMenu(t, m.Notify.Normalized()), languageMenu(t, m.Language),
		Item{ID: CmdAutostart, Label: t.T("autostart"), Checked: m.Autostart, Icon: "startup"})

	// updates, log, quit
	items = append(items, Separator, checkUpdateItem(t, m.Checking),
		Item{ID: CmdSystemMenu, Label: t.T("system_menu"), Checked: m.SystemMenu, Icon: "window"},
		Item{ID: CmdLog, Label: t.T("log"), Icon: "log"},
		Item{ID: CmdQuit, Label: t.T("quit"), Icon: "quit"})
	return items
}

func statusIcon(m MenuState, linked bool) string {
	switch {
	case !linked || m.Status.State == Failed:
		return "status-off"
	case m.Status.Paused || m.Status.Offline:
		return "status-wait"
	}
	return "status-on"
}

// foldersMenu is "Manage folders": each watched folder (its type, path and how it is watched) with
// Open folder and Stop watching; looking for folders again; adding one by hand; and the folders the
// player stopped watching, each with Watch again.
func foldersMenu(t Text, m MenuState) Item {
	var sub []Item
	for i, f := range Folders(m.Summaries) {
		sub = append(sub, Item{Label: f.Types(t), Detail: f.Path, Note: f.Status(t, m.Status), Icon: "folder",
			Sub: []Item{
				{ID: CmdFolderOpenBase + i, Label: t.T("folder_open"), Icon: "open"},
				{ID: CmdFolderStopBase + i, Label: t.T("folder_stop"), Icon: "stop", Danger: true},
			}})
	}
	if len(sub) == 0 {
		sub = append(sub, Item{Label: t.T("no_sources"), Disabled: true, Status: true})
	}
	sub = append(sub, Separator,
		Item{ID: CmdDetect, Label: t.T("detect_again"), Icon: "search"},
		Item{ID: CmdAddFolder, Label: t.T("add_folder"), Icon: "add"})
	if len(m.Stopped) > 0 {
		var stopped []Item
		for i, f := range m.Stopped {
			stopped = append(stopped, Item{ID: CmdFolderRestoreBase + i, Label: t.T("folder_watch_again"),
				Detail: f.Path, Note: StoppedTypes(t, f), Icon: "restore"})
		}
		sub = append(sub, Item{Label: t.T("folders_stopped", len(m.Stopped)), Icon: "history", Sub: stopped})
	}
	return Item{Label: t.T("manage_folders"), Icon: "folder", Sub: sub}
}

func checkUpdateItem(t Text, checking bool) Item {
	if checking {
		return Item{ID: CmdCheckUpdate, Label: t.T("update_checking"), Disabled: true, Icon: "refresh"}
	}
	return Item{ID: CmdCheckUpdate, Label: t.T("update_check"), Icon: "refresh"}
}

// notifyMenu is the "Notifications" submenu: style, sound, corner and a test.
func notifyMenu(t Text, p config.NotifyPrefs) Item {
	corners := make([]Item, 0, len(config.Corners))
	for i, c := range config.Corners {
		corners = append(corners, Item{ID: CmdCornerBase + i, Label: t.T("corner_" + c), Checked: p.Corner == c,
			Disabled: p.Style != config.StyleVigil})
	}
	volumes := make([]Item, 0, len(config.Volumes))
	for i, v := range config.Volumes {
		volumes = append(volumes, Item{ID: CmdVolumeBase + i, Label: t.T("vol_" + v), Checked: p.Volume == v})
	}
	return Item{Label: t.T("notifications"), Icon: "bell", Sub: []Item{
		{ID: CmdNotifyVigil, Label: t.T("notify_vigil"), Checked: p.Style == config.StyleVigil},
		{ID: CmdNotifyWindows, Label: t.T("notify_windows"), Checked: p.Style == config.StyleWindows},
		{ID: CmdNotifyOff, Label: t.T("notify_off"), Checked: p.Style == config.StyleOff},
		Separator,
		{Label: t.T("sound"), Sub: volumes, Disabled: p.Style == config.StyleOff},
		{Label: t.T("position"), Sub: corners},
		Separator,
		{ID: CmdNotifyTest, Label: t.T("notify_test")},
	}}
}

// languageMenu is the "Language" submenu: Automatic (follow Windows), English, Português (Brasil).
func languageMenu(t Text, pref string) Item {
	if pref == "" {
		pref = "auto"
	}
	sub := make([]Item, 0, len(LanguageChoices))
	for i, l := range LanguageChoices {
		sub = append(sub, Item{ID: CmdLangBase + i, Label: t.T("lang_" + l), Checked: pref == l})
	}
	return Item{Label: t.T("language"), Icon: "globe", Sub: sub}
}

// NewCandidates drops the detected sources that are already configured.
func NewCandidates(store *config.Store, found []detect.Candidate) []detect.Candidate {
	configured := map[string]bool{}
	for _, c := range store.Sources() {
		configured[SourceKey(c)] = true
	}
	var out []detect.Candidate
	for _, c := range found {
		if !configured[SourceKey(config.SourceConfig{Type: c.Type, Path: c.Path})] {
			out = append(out, c)
		}
	}
	return out
}

// AddCandidates saves the sources the user accepted; returns how many were new.
func AddCandidates(store *config.Store, cands []detect.Candidate) (int, error) {
	added := 0
	var errs []error
	for _, c := range cands {
		ok, err := store.AddSource(config.SourceConfig{Type: c.Type, Path: c.Path, GameDir: c.GameDir})
		if err != nil {
			errs = append(errs, err)
		}
		if ok {
			added++
		}
	}
	return added, errors.Join(errs...)
}
