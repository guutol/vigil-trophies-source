//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// detach starts the game in its own process group without the agent's console, so Ctrl+C or
// closing the agent's window never reaches it.
func detach(cmd *exec.Cmd) {
	const detachedProcess = 0x00000008 // DETACHED_PROCESS
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}
