//go:build windows

package app

import (
	"errors"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// RunKeyPath is the current user's "start with Windows" list (no admin rights needed).
const RunKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// RegistryRunKey is a RunKey under HKEY_CURRENT_USER.
type RegistryRunKey struct {
	Path string
}

// Get implements RunKey.
func (k RegistryRunKey) Get(name string) (string, bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, k.Path, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer key.Close()
	v, _, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	return v, err == nil, err
}

// Set implements RunKey.
func (k RegistryRunKey) Set(name, value string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, k.Path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(name, value)
}

// Delete implements RunKey.
func (k RegistryRunKey) Delete(name string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, k.Path, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// SingleInstance takes a per-user named mutex. first is false when another copy already holds
// it; release frees it (call it when quitting).
func SingleInstance(name string) (release func(), first bool, err error) {
	p, err := windows.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateMutex(nil, false, p)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(h)
		return func() {}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return func() { windows.CloseHandle(h) }, true, nil
}

var pGetUserDefaultUILanguage = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")

// UserLocale is the Windows display language as "pt-BR" (any Portuguese) or "en"
// (GetUserDefaultUILanguage), for i18n.Resolve.
func UserLocale() string {
	id, _, _ := pGetUserDefaultUILanguage.Call()
	const langPortuguese = 0x16
	if id&0x3ff == langPortuguese {
		return "pt-BR"
	}
	return "en"
}
