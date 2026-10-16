//go:build !windows

package main

import (
	"errors"
	"io"

	"github.com/vigil-tracker/agent/internal/config"
)

// notifyTest: the trophy notifications are a Windows feature (the tray app).
func notifyTest(_ *config.Store, _ []string, _ io.Writer) error {
	return errors.New("notify-test is only available on Windows (the notifications belong to the tray app)")
}
