//go:build desktop

package desktop

import (
	"context"
	"path/filepath"
	"time"

	"github.com/devenjarvis/lathe/internal/buildinfo"
	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/devenjarvis/lathe/internal/update"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// The in-app update check (#149): notify-only. Lathe › Check for updates…
// asks GitHub now and answers in a dialog; with Settings › Library › "Check
// for updates automatically" on (the default), the app also asks at most
// once a day, and the pages show a quiet title-bar notice from the cached
// answer (serve.Updates). Nothing is downloaded or replaced.

const (
	// updateFirstDelay lets the window come up before the first lookup.
	updateFirstDelay = 20 * time.Second
	// updateTick is how often the loop asks the checker whether a day has
	// passed (and re-reads the Settings toggle).
	updateTick = time.Hour
)

// updater is the shell's serve.Updates and the menu item's handler.
type updater struct {
	app  *application.App
	sh   *shell
	chk  *update.Checker
	logf func(string, ...any)
}

var _ serve.Updates = (*updater)(nil)

func newUpdater(app *application.App, sh *shell, logf func(string, ...any)) *updater {
	current := buildinfo.Resolve()
	chk := &update.Checker{
		Current: current,
		Fetch:   update.GitHub(nil, update.LatestURL, current),
		Install: update.Detect(),
	}
	if dir, err := config.ConfigDir(); err == nil {
		chk.CachePath = filepath.Join(dir, "update-check.json")
	}
	return &updater{app: app, sh: sh, chk: chk, logf: logf}
}

// Notice is the title-bar notice, from the cache (no network).
func (u *updater) Notice() *update.Notice { return u.chk.Notice() }

// Dismiss hides the notice until a newer version.
func (u *updater) Dismiss(version string) error { return u.chk.Dismiss(version) }

// Restart reopens the upgraded app bundle once this process has exited, then
// quits.
func (u *updater) Restart() error {
	if err := relaunch(u.chk.Install.Bundle); err != nil {
		return err
	}
	go u.app.Quit()
	return nil
}

// loop runs the automatic checks until ctx ends. A development build never
// checks; a failed lookup is logged and retried on a later tick.
func (u *updater) loop(ctx context.Context) {
	if u.chk.Dev() {
		u.logf("update checks are off in a development build (%s)", u.chk.Current)
		return
	}
	wait := updateFirstDelay
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = updateTick
		if !config.UpdateCheck() || !u.chk.Due() {
			continue
		}
		res, err := u.chk.Check(ctx, false)
		if err != nil {
			u.logf("could not check for updates: %v", err)
			continue
		}
		if n := res.Notice; n != nil {
			u.logf("Lathe %s is available (this is %s)", n.Version, res.Current)
		}
	}
}

// checkNow is Lathe › Check for updates…: ask GitHub now (whatever the
// Settings toggle says, since the reader asked) and answer in a dialog. It
// blocks on the network, so the menu calls it on its own goroutine.
func (u *updater) checkNow() {
	ctx, cancel := context.WithTimeout(context.Background(), update.FetchTimeout+5*time.Second)
	defer cancel()
	res, err := u.chk.Check(ctx, true)
	d := updateDialogFor(res, err)

	dlg := u.app.Dialog.Info()
	if d.Warning {
		dlg = u.app.Dialog.Warning()
	}
	dlg.SetTitle(d.Title).SetMessage(d.Message)
	if d.Button != "" {
		dlg.AddButton(d.Button).SetAsDefault().OnClick(func() { u.act(d) })
	}
	dismiss := dlg.AddButton(d.Dismiss).SetAsCancel()
	if d.Button == "" {
		dismiss.SetAsDefault()
	}
	if w := u.sh.parentWindow(); w != nil {
		dlg.AttachToWindow(w)
	}
	dlg.Show()
}

// act carries out the dialog's main button.
func (u *updater) act(d updateDialog) {
	var err error
	switch d.Action {
	case updateActDownload:
		err = u.sh.OpenURL(d.Arg)
	case updateActCopy:
		if !u.app.Clipboard.SetText(d.Arg) {
			u.logf("could not copy %q to the clipboard", d.Arg)
		}
	case updateActRestart:
		err = u.Restart()
	}
	if err != nil {
		u.logf("update: %v", err)
	}
}
