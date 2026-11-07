//go:build desktop

package desktop

import (
	"fmt"
	"sync"

	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// shell is the app's serve.Native: what the reader can ask of the OS through
// the server. The server calls it from its HTTP handler goroutines (the Wails
// asset server runs every request on its own goroutine), never from the UI
// thread. Every Wails call used here marshals itself onto the main thread
// (InvokeSync / InvokeAsync) and hands results back over a channel, so
// calling them from a handler goroutine is safe, and blocking that goroutine
// on a dialog does not block the UI.
type shell struct {
	app   *application.App
	notes *notifier
	logf  func(string, ...any)

	mu      sync.Mutex
	main    *application.WebviewWindow
	menu    *menuBar
	picking sync.Mutex // one folder dialog at a time
}

var (
	_ serve.Native     = (*shell)(nil)
	_ serve.NativeMenu = (*shell)(nil)
)

func (s *shell) setMenu(m *menuBar) {
	s.mu.Lock()
	s.menu = m
	s.mu.Unlock()
}

// SetMenuState enables and disables the menu bar's page items for the
// focused page (see menuBar). It never blocks.
func (s *shell) SetMenuState(st serve.MenuState) {
	s.mu.Lock()
	m := s.menu
	s.mu.Unlock()
	if m != nil {
		m.setState(st)
	}
}

func (s *shell) setMain(w *application.WebviewWindow) {
	s.mu.Lock()
	s.main = w
	s.mu.Unlock()
}

func (s *shell) mainWindow() *application.WebviewWindow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.main
}

// focusMain brings the reader window forward (a second `lathe` launch).
func (s *shell) focusMain() {
	w := s.mainWindow()
	if w == nil {
		return
	}
	w.Restore()
	w.Show()
	w.Focus()
}

// PickFolder shows the OS directory chooser over the window the reader is
// using and blocks until it closes. A cancel returns "".
func (s *shell) PickFolder(title, start string) (string, error) {
	s.picking.Lock()
	defer s.picking.Unlock()

	d := s.app.Dialog.OpenFile().
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true).
		ShowHiddenFiles(false)
	if title != "" {
		d.SetTitle(title)
	}
	if start != "" {
		d.SetDirectory(start)
	}
	if w := s.parentWindow(); w != nil {
		d.AttachToWindow(w)
	}
	return d.PromptForSingleSelection()
}

// parentWindow is the focused app window (the pop-out when the reader picks
// from there), else the main window.
func (s *shell) parentWindow() application.Window {
	if w := s.app.Window.Current(); w != nil {
		return w
	}
	if w := s.mainWindow(); w != nil {
		return w
	}
	return nil
}

// Notify posts a system notification without blocking (see notifier).
func (s *shell) Notify(title, body string) {
	s.notes.Notify(title, body)
}

// OpenURL opens an external link in the system browser (or mail app). The
// server already checked it; check again so the shell never launches a
// file: or custom-scheme handler whatever calls it.
func (s *shell) OpenURL(raw string) error {
	target, ok := serve.ExternalURL(raw)
	if !ok {
		return fmt.Errorf("not an external http, https or mailto URL: %q", raw)
	}
	return s.app.Browser.OpenURL(target)
}

// OpenWindow opens path in a new app window, or focuses the window already
// showing it (clicking "Pop out" twice gives one pop-out).
func (s *shell) OpenWindow(path, title string) error {
	path, err := windowPath(path)
	if err != nil {
		return err
	}
	if title == "" {
		title = AppName
	}
	name, settings := windowKey(path)
	if w, ok := s.app.Window.GetByName(name); ok {
		if settings {
			// Already open: go to the asked-for tab.
			if ww, ok := w.(*application.WebviewWindow); ok {
				ww.SetURL(path)
			}
		}
		w.Restore()
		w.Show()
		w.Focus()
		return nil
	}
	width, height, minW, minH := popWidth, popHeight, popMinWidth, popMinHeight
	if settings {
		width, height, minW, minH = settingsWidth, settingsHeight, settingsMinWidth, settingsMinHeight
	}
	s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             name,
		Title:            title,
		URL:              path,
		Width:            width,
		Height:           height,
		MinWidth:         minW,
		MinHeight:        minH,
		BackgroundColour: paper,
		Linux:            application.LinuxWindow{Icon: appIcon},
		// The menu bar carries the shortcuts on Windows and Linux too, where
		// it is per window (the page binds none of the menu's keys).
		UseApplicationMenu: true,
	})
	return nil
}

// openSettings shows the Settings window at href ("/-/settings", or a tab
// such as "/-/settings#agent"). OpenWindow reuses the one Settings window
// and moves it to the asked-for tab. Lathe › Settings… (⌘,) and Agent ›
// Agent settings… call it.
func (s *shell) openSettings(href string) error {
	return s.OpenWindow(href, "Settings")
}

// RevealFolder shows a folder in the system file manager (Finder on macOS).
// It makes the shell a serve.FolderRevealer, for Settings › Library's "Show
// in Finder".
func (s *shell) RevealFolder(path string) error {
	return s.app.Browser.OpenFile(path)
}

var _ serve.FolderRevealer = (*shell)(nil)
