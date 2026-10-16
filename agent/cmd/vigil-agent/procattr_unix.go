//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach starts the game in a new session (like the bbport launcher's setsid), so Ctrl+C in the
// agent's terminal or closing it never reaches the game.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
