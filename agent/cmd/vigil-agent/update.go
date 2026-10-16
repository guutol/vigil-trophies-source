package main

import (
	"context"
	"fmt"
	"io"
	"runtime"

	"github.com/vigil-tracker/agent/internal/api"
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/update"
)

// checkUpdate asks the official distribution repository for a newer version and prints the official
// download for this platform. It never downloads or replaces anything.
func checkUpdate(ctx context.Context, store *config.Store, out io.Writer) error {
	if _, ok := update.Parse(api.Version); !ok {
		fmt.Fprintf(out, "development build (%s): updates are not checked\n", api.Version)
		return nil
	}
	load, save := update.FileState(store.UpdatePath())
	c := &update.Checker{Current: api.Version, Asset: update.AssetFor(runtime.GOOS, runtime.GOARCH, false), Load: load, Save: save}
	res, err := c.Check(ctx, true)
	switch {
	case err != nil:
		return fmt.Errorf("cannot check for updates: %w", err)
	case res.Available:
		fmt.Fprintf(out, "version %s is available (you have %s)\ndownload: %s\n", res.Latest, res.Current, res.URL)
		_ = c.MarkNotified(res.Latest)
	default:
		fmt.Fprintf(out, "you have the latest version (%s)\n", res.Current)
	}
	return nil
}
