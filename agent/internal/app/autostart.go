package app

import (
	"errors"
	"strings"
)

// RunKey is the per-user "start with Windows" list (HKCU\Software\Microsoft\Windows\CurrentVersion\Run
// on Windows; a map in tests).
type RunKey interface {
	// Get returns the value, or ok=false when it does not exist.
	Get(name string) (value string, ok bool, err error)
	Set(name, value string) error
	// Delete removes the value; a missing value is not an error.
	Delete(name string) error
}

// AutostartName is the value name under the Run key.
const AutostartName = "VigilTrophies"

// Autostart turns "start with Windows" on and off for the current user only (no admin rights).
type Autostart struct {
	Key RunKey
	// Exe is the absolute path of vigil-agent-tray.exe.
	Exe string
}

// Command is what goes into the Run key: the quoted path (paths with spaces must be quoted).
func (a Autostart) Command() string { return `"` + a.Exe + `"` }

// Enabled is true when the Run key starts this same executable.
func (a Autostart) Enabled() (bool, error) {
	v, ok, err := a.Key.Get(AutostartName)
	if err != nil || !ok {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(v), a.Command()), nil
}

// SetEnabled writes or removes the value.
func (a Autostart) SetEnabled(on bool) error {
	if !on {
		return a.Key.Delete(AutostartName)
	}
	if a.Exe == "" || strings.ContainsAny(a.Exe, "\"\r\n") {
		return errors.New("invalid executable path")
	}
	return a.Key.Set(AutostartName, a.Command())
}
