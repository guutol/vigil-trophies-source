package app

import (
	"os"
	"strings"

	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/source/bbportlog"
	"github.com/vigil-tracker/agent/internal/source/detect"
)

// Folder is one watched folder as the player sees it: bbport's user folder can hold both the patch
// file and the log, and it is still one folder to stop or keep watching.
type Folder struct {
	Path    string
	Sources []Summary
}

// Folders groups the source summaries by folder, in the order they were configured.
func Folders(sums []Summary) []Folder {
	var out []Folder
	for _, s := range sums {
		placed := false
		for i := range out {
			if config.SameFolder(out[i].Path, s.Config.Path) {
				out[i].Sources = append(out[i].Sources, s)
				placed = true
				break
			}
		}
		if !placed {
			out = append(out, Folder{Path: s.Config.Path, Sources: []Summary{s}})
		}
	}
	return out
}

// Types is "bbport (log) + bbport (patch)".
func (f Folder) Types(t Text) string {
	labels := make([]string, 0, len(f.Sources))
	for _, s := range f.Sources {
		labels = append(labels, t.Source(s.Config.Type))
	}
	return strings.Join(labels, " + ")
}

// Counts adds up the folder's games and trophies.
func (f Folder) Counts() (games, unlocks int) {
	for _, s := range f.Sources {
		games += s.Games
		unlocks += s.Unlocks
	}
	return games, unlocks
}

// Status says, from what is actually known, how the folder is being watched: the folder is gone,
// it could not be read, syncing is paused or waits for the PC to be linked, or it is watched (with
// what it holds). Nothing is claimed that the app did not check.
func (f Folder) Status(t Text, st Status) string {
	if info, err := os.Stat(f.Path); err != nil || !info.IsDir() {
		return t.T("folder_status_missing")
	}
	for _, s := range f.Sources {
		if bbportlog.IsUnidentified(s.Err) {
			return t.T("folder_status_gow3_unidentified")
		}
	}
	for _, s := range f.Sources {
		if s.Err != nil {
			return t.T("folder_status_error")
		}
	}
	games, unlocks := f.Counts()
	counts := t.N("games", games) + ", " + t.N("trophies", unlocks)
	// Different readers of one folder can report the same trophy. Do not present a sum as unique.
	if len(f.Sources) > 1 {
		counts = ""
	}
	if counts != "" {
		counts = " · " + counts
	}
	switch {
	case st.State == NotLinked || st.State == Unauthorized:
		return t.T("folder_status_unlinked") + counts
	case st.Paused:
		return t.T("folder_status_paused") + counts
	case st.State != Running:
		return t.T("folder_status_idle") + counts
	}
	return t.T("folder_status_watching") + counts
}

// WithoutStopped drops detected folders the player stopped watching: detection never offers them
// again (the stop is undone only by an explicit "Watch again" or by adding the folder by hand).
func WithoutStopped(found []detect.Candidate, stopped []config.StoppedFolder) []detect.Candidate {
	if len(stopped) == 0 {
		return found
	}
	out := found[:0:0]
	for _, c := range found {
		skip := false
		for _, s := range stopped {
			if config.SameFolder(s.Path, c.Path) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, c)
		}
	}
	return out
}

// StoppedTypes is the source types that were configured in a stopped folder.
func StoppedTypes(t Text, f config.StoppedFolder) string {
	labels := make([]string, 0, len(f.Sources))
	for _, s := range f.Sources {
		labels = append(labels, t.Source(s.Type))
	}
	return strings.Join(labels, " + ")
}
