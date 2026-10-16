//go:build !windows

// Command vigil-agent-tray is the Windows tray app. On Linux and the Steam Deck the command line
// (vigil-agent) is the way to go.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "vigil-agent-tray is Windows only; use vigil-agent (see vigil-agent help)")
	os.Exit(2)
}
