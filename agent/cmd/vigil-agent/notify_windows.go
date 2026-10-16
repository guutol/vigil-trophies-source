//go:build windows

package main

import (
	"errors"
	"flag"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/vigil-tracker/agent/internal/app"
	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/i18n"
	"github.com/vigil-tracker/agent/internal/notify"
	"github.com/vigil-tracker/agent/internal/tray"
)

// notifyTest shows example trophy notifications. Nothing is sent to the server.
//
//	vigil-agent notify-test [--style vigil|windows] [--grade bronze|silver|gold|platinum]
func notifyTest(store *config.Store, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("notify-test", flag.ContinueOnError)
	prefs := store.NotifyPrefs()
	defStyle := prefs.Style
	if defStyle == config.StyleOff {
		defStyle = config.StyleVigil
	}
	style := fs.String("style", defStyle, "vigil (the agent's popup) or windows (a Windows notification)")
	grade := fs.String("grade", "", "only this grade: bronze, silver, gold or platinum (default: all, then a summary)")
	render := fs.String("render", "", "save each card as a PNG in this folder instead of showing it (screenshots)")
	scale := fs.Float64("scale", 1, "with --render: DPI scale (1 = 96 DPI)")
	langFlag := fs.String("lang", "", "texts in this language (pt-BR or en; default: the Windows language)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *style != config.StyleVigil && *style != config.StyleWindows {
		return errors.New("--style must be vigil or windows")
	}
	lang := i18n.Resolve(store.Language(), app.UserLocale())
	if *langFlag != "" {
		lang = i18n.FromLocale(*langFlag)
	}
	items := append(notify.Samples(lang), notify.SampleSummary())
	if *grade != "" {
		if !slices.Contains(notify.Grades, *grade) {
			return errors.New("--grade must be bronze, silver, gold or platinum")
		}
		items = []notify.Item{notify.Samples(lang)[slices.Index(notify.Grades, *grade)]}
	}

	if *render != "" {
		if *scale < 0.5 || *scale > 4 {
			return errors.New("--scale must be between 0.5 and 4")
		}
		if err := os.MkdirAll(*render, 0o755); err != nil {
			return err
		}
		for _, it := range items {
			name := it.Grade
			if it.Summary {
				name = "summary"
			}
			path := filepath.Join(*render, "notification-"+name+".png")
			if err := renderPNG(it, lang, *scale, path); err != nil {
				return err
			}
			fmt.Fprintln(out, path)
		}
		return nil
	}

	if *style == config.StyleWindows {
		t := &tray.Tray{Tooltip: "Vigil Trophies"}
		done := make(chan error, 1)
		go func() { done <- t.Run() }()
		for _, it := range items {
			title := notify.T(lang, "windows_title", it.Trophy)
			if it.Summary {
				title = notify.Title(lang, it)
			}
			t.NotifyIcon(title, notify.Subtitle(lang, it), it.Icon, true)
			notify.PlaySound(it.IsPlatinum(), prefs.Volume)
			fmt.Fprintf(out, "shown: %s\n", title)
			time.Sleep(4 * time.Second)
		}
		t.Quit()
		return <-done
	}

	p, err := notify.NewPresenter(lang, func() config.NotifyPrefs { return prefs }, nil)
	if err != nil {
		return err
	}
	defer p.Close()
	p.Log = func(format string, args ...any) { fmt.Fprintln(os.Stderr, fmt.Sprintf(format, args...)) }
	p.ShowInOrder(items...)
	fmt.Fprintf(out, "showing %d notification(s) in the %s corner...\n", len(items), prefs.Corner)
	if !p.WaitIdle(time.Duration(len(items)) * 8 * time.Second) {
		return errors.New("the notifications did not finish in time")
	}
	return nil
}

func renderPNG(it notify.Item, lang string, scale float64, path string) error {
	c, err := notify.Render(it, lang, scale)
	if err != nil {
		return err
	}
	defer c.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, c.NRGBA()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
