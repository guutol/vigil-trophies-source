package app

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/source/detect"
)

// DetectVersion is bumped whenever detection learns to find sources it used to miss or to
// mislabel. The tray app offers what changed once to installs that saw an older version.
//
//	2: portable bbport (bbport-windows/user, before its first log; the launcher's game folder),
//	   and bbport user folders are no longer taken for shadPS4.
//	3: the God of War III port (gow3_pc): its launcher's saves folder, its package, the AppImage
//	   data folder; its saves folder is no longer taken for bbport.
//
// 4: Shadow of the Colossus (AjncSystem), with its own identity and savedata normalization.
const DetectVersion = 4

// Offer is one folder in the "Sources found" dialog: every source type found in it (bbport's
// user folder can hold both the patch file and the log), and what it holds. Replaces: configured
// sources of this folder that detection now recognizes as something else (a bbport folder saved
// as shadPS4 by an older version); accepting the offer removes them.
type Offer struct {
	Path       string
	Candidates []detect.Candidate
	Games      int
	Unlocks    int
	Replaces   []config.SourceConfig
}

// Types lists the offer's source types.
func (o Offer) Types() []string {
	out := make([]string, 0, len(o.Candidates))
	for _, c := range o.Candidates {
		out = append(out, c.Type)
	}
	return out
}

// Title is "bbport (patch) + bbport (log) — 1 game, 12 trophies".
func (o Offer) Title(t Text) string {
	labels := make([]string, 0, len(o.Candidates))
	for _, c := range o.Candidates {
		labels = append(labels, t.Source(c.Type))
	}
	line := t.T("source_line", strings.Join(labels, " + "), t.N("games", o.Games), t.N("trophies", o.Unlocks))
	if len(o.Replaces) > 0 {
		old := make([]string, 0, len(o.Replaces))
		for _, r := range o.Replaces {
			old = append(old, t.Source(r.Type))
		}
		line += " " + t.T("source_replaces", strings.Join(old, " + "))
	}
	return line
}

// Plan is what detection would change in the configured sources: candidates not configured yet,
// and configured sources that detection now gives another type in the same folder.
type Plan struct {
	Add     []detect.Candidate
	Replace []config.SourceConfig
}

// PlanDetection compares the configured sources with what detection finds now. A configured
// source is only replaced when its own folder is found with other types and never with its own:
// a folder detection does not recognize at all is left as the user set it.
func PlanDetection(configured []config.SourceConfig, found []detect.Candidate) Plan {
	foundTypes := map[string]map[string]bool{}
	for _, c := range found {
		key := strings.ToLower(filepath.Clean(c.Path))
		if foundTypes[key] == nil {
			foundTypes[key] = map[string]bool{}
		}
		foundTypes[key][c.Type] = true
	}
	var plan Plan
	have := map[string]bool{}
	for _, c := range configured {
		have[SourceKey(c)] = true
		types := foundTypes[strings.ToLower(filepath.Clean(c.Path))]
		if len(types) > 0 && !types[c.Type] {
			plan.Replace = append(plan.Replace, c)
		}
	}
	for _, c := range found {
		if !have[SourceKey(config.SourceConfig{Type: c.Type, Path: c.Path})] {
			plan.Add = append(plan.Add, c)
		}
	}
	return plan
}

// MarkReplacements attaches to each offer the configured sources of its folder it replaces.
// Replacements whose folder is not offered (left out by BuildOffers) are not made.
func MarkReplacements(offers []Offer, replace []config.SourceConfig) []Offer {
	for i := range offers {
		for _, r := range replace {
			if SameFile(r.Path, offers[i].Path) {
				offers[i].Replaces = append(offers[i].Replaces, r)
			}
		}
	}
	return offers
}

// ApplyOffers saves the accepted offers: the sources they replace are removed first, then the new
// candidates are added. Returns how many sources were added and removed.
func ApplyOffers(store *config.Store, offers []Offer) (added, removed int, err error) {
	var errs []error
	for _, o := range offers {
		for _, r := range o.Replaces {
			n, err := store.RemoveSource(r)
			removed += n
			if err != nil {
				errs = append(errs, err)
			}
		}
	}
	added, err = AddCandidates(store, OfferCandidates(offers))
	if err != nil {
		errs = append(errs, err)
	}
	return added, removed, errors.Join(errs...)
}

// BuildOffers groups the detected sources by folder and keeps the ones worth offering: those that
// already hold trophies, or that surely belong to the program (Candidate.Certain). summarize says
// what a candidate holds (Summarize in production).
func BuildOffers(found []detect.Candidate, summarize func(detect.Candidate) Summary) []Offer {
	byPath := map[string]*Offer{}
	var order []string
	for _, c := range found {
		sum := summarize(c)
		if !c.Certain && sum.Games == 0 {
			continue
		}
		key := strings.ToLower(filepath.Clean(c.Path))
		o, ok := byPath[key]
		if !ok {
			o = &Offer{Path: c.Path}
			byPath[key] = o
			order = append(order, key)
		}
		o.Candidates = append(o.Candidates, c)
		// The patch file and the log of one folder hold the same trophies: count them once.
		o.Games = max(o.Games, sum.Games)
		o.Unlocks = max(o.Unlocks, sum.Unlocks)
	}
	out := make([]Offer, 0, len(order))
	for _, k := range order {
		o := *byPath[k]
		sort.Slice(o.Candidates, func(i, j int) bool { return o.Candidates[i].Type < o.Candidates[j].Type })
		out = append(out, o)
	}
	// Folders with trophies first, then by path.
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Unlocks > 0) != (out[j].Unlocks > 0) {
			return out[i].Unlocks > 0
		}
		return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path)
	})
	return out
}

// OfferCandidates flattens offers back into the candidates to save.
func OfferCandidates(offers []Offer) []detect.Candidate {
	var out []detect.Candidate
	for _, o := range offers {
		out = append(out, o.Candidates...)
	}
	return out
}

// SummarizeCandidate is BuildOffers' summarize in production (read only, 10 s per source at most).
func SummarizeCandidate(c detect.Candidate) Summary {
	return Summarize(context.Background(), config.SourceConfig{Type: c.Type, Path: c.Path, GameDir: c.GameDir, UpdateDir: c.UpdateDir})
}
