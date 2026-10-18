//go:build windows

package detect

import (
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// gamesFolders is <drive>:\Games on every fixed local drive (no network, removable or optical
// drive, so detection never waits on a slow or empty device). Only folders that exist are kept.
func gamesFolders(homeVolume string) []string {
	var out []string
	if homeVolume != "" && isDir(homeVolume+`\Games`) {
		out = append(out, homeVolume+`\Games`)
	}
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return out
	}
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, err := windows.UTF16PtrFromString(root)
		if err != nil || windows.GetDriveType(p) != windows.DRIVE_FIXED {
			continue
		}
		dir := root + "Games"
		if strings.EqualFold(filepath.VolumeName(dir), homeVolume) {
			continue // already in the list
		}
		if isDir(dir) {
			out = append(out, dir)
		}
	}
	return out
}

// gow3Processes are the executables of the God of War III port (the launcher, the shortcut player
// and the game itself, in bin\).
var gow3Processes = map[string]bool{
	"god of war iii.exe":      true,
	"play god of war iii.exe": true,
	"gow3-probe.exe":          true,
}

// runningGOW3Dirs is the port folder of every God of War III port running now: its executable's
// folder (the parent of bin\ for the game itself). Only names and paths are read; nothing is
// opened for writing, and a process that cannot be queried is skipped.
func runningGOW3Dirs() []string { return runningPortDirs(gow3Processes) }
func runningSOTCDirs() []string {
	return runningPortDirs(map[string]bool{"sotc.exe": true, "bb-probe.exe": true})
}
func runningPortDirs(names map[string]bool) []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var out []string
	seen := map[string]bool{}
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if !names[strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))] {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		size := uint32(len(buf))
		err = windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
		windows.CloseHandle(h)
		if err != nil {
			continue
		}
		dir := filepath.Dir(windows.UTF16ToString(buf[:size]))
		if strings.EqualFold(filepath.Base(dir), "bin") {
			dir = filepath.Dir(dir)
		}
		if key := strings.ToLower(dir); !seen[key] {
			seen[key] = true
			out = append(out, dir)
		}
	}
	return out
}
