// Package update tells a player that a newer Vigil is out. It reads the public list of releases
// (betas included) from the official distribution repository, compares versions, and hands back the
// official download address for this platform. It never downloads, replaces or runs anything: the
// player clicks and the browser opens the download.
package update

import (
	"strconv"
	"strings"
)

// Version is a parsed vX.Y.Z or vX.Y.Z-pre.release tag (semantic versioning precedence).
type Version struct {
	Major, Minor, Patch int
	Pre                 []string // "beta", "10"; empty for a stable release
	Raw                 string   // the tag as written
}

// Parse reads "v1.2.3", "1.2.3" or "v1.2.3-beta.10". Build metadata (+...) is ignored.
// ok is false for anything else ("dev", empty, garbage).
func Parse(tag string) (Version, bool) {
	s := strings.TrimSpace(tag)
	raw := s
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return Version{}, false
		}
		nums[i] = n
	}
	v := Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Raw: raw}
	if hasPre {
		if pre == "" {
			return Version{}, false
		}
		for _, id := range strings.Split(pre, ".") {
			if id == "" {
				return Version{}, false
			}
			v.Pre = append(v.Pre, id)
		}
	}
	return v, true
}

// Compare returns -1, 0 or 1. A stable release is newer than any pre-release of the same X.Y.Z;
// numeric identifiers compare as numbers (beta.10 > beta.9), others as text, and a numeric one is
// older than a text one (semver 2.0 §11).
func Compare(a, b Version) int {
	for _, d := range [3]int{a.Major - b.Major, a.Minor - b.Minor, a.Patch - b.Patch} {
		if d != 0 {
			return sign(d)
		}
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		if c := compareID(a.Pre[i], b.Pre[i]); c != 0 {
			return c
		}
	}
	return sign(len(a.Pre) - len(b.Pre))
}

func compareID(x, y string) int {
	nx, ex := strconv.Atoi(x)
	ny, ey := strconv.Atoi(y)
	switch {
	case ex == nil && ey == nil:
		return sign(nx - ny)
	case ex == nil:
		return -1
	case ey == nil:
		return 1
	}
	return strings.Compare(x, y)
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}

// Newer reports whether candidate is a newer version than current. An unparseable current version
// (a development build: "dev") is never told to update.
func Newer(candidate, current string) bool {
	c, ok1 := Parse(candidate)
	cur, ok2 := Parse(current)
	return ok1 && ok2 && Compare(c, cur) > 0
}
