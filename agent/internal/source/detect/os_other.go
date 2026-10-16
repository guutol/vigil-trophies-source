//go:build !windows

package detect

// gamesFolders: drive letters are a Windows idea; the usual Linux folders are looked at elsewhere.
func gamesFolders(string) []string { return nil }

// runningGOW3Dirs: the AppImage's data folder and the launcher's settings are enough on Linux.
func runningGOW3Dirs() []string { return nil }
