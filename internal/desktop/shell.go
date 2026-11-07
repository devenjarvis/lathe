//go:build desktop

package desktop

import (
	"fmt"
	"sync"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
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

	srv   *serve.Server
	badge *badgeService

	mu      sync.Mutex
	main    *application.WebviewWindow
	menu    *menuBar
	picking sync.Mutex // one folder dialog at a time
	// tracked are the windows reopened on the next launch (the main window
	// and tutorial windows), by name; quitting freezes them, so the windows
	// closing as the app quits stay on the list (see saveWindows).
	tracked  map[string]*trackedWindow
	quitting bool
	saved    bool
}

// trackedWindow is a window saveWindows records.
type trackedWindow struct {
	win    *application.WebviewWindow
	main   bool
	path   string // the page it opened at
	bounds rect   // the latest bounds, kept current from move/resize events
}

var (
	_ serve.Native         = (*shell)(nil)
	_ serve.NativeMenu     = (*shell)(nil)
	_ serve.ActionNotifier = (*shell)(nil)
	_ serve.Badger         = (*shell)(nil)
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

// NotifyWithAction posts a notification with a button that opens a Lathe
// page (serve.ActionNotifier). It never blocks; where the platform shows no
// button it is a plain notification.
func (s *shell) NotifyWithAction(n serve.Notice) {
	if n.Action == nil {
		s.notes.Notify(n.Title, n.Body)
		return
	}
	s.notes.NotifyAction(n.Title, n.Body, n.Action.Label, n.Action.Path)
}

// SetBadge shows the count of things waiting on the reader on the app icon
// (serve.Badger). A no-op where the platform has no badge.
func (s *shell) SetBadge(count int) {
	if s.badge != nil {
		s.badge.set(count)
	}
}

// showPath opens a page a notification points at: in its tutorial's own
// window when one is open, else in the main window. Only Lathe's reading
// pages and library views are opened, whatever the notification carried.
func (s *shell) showPath(path string) {
	if _, err := windowPath(path); err != nil || s.srv == nil {
		return
	}
	slug, _, ok := s.srv.TutorialPage(path)
	if !ok && !serve.HomePage(path) {
		s.logf("notification asked to open %q; ignored", path)
		return
	}
	if ok {
		if w, found := s.app.Window.GetByName(tutorialWindowName(slug)); found {
			if ww, isWebview := w.(*application.WebviewWindow); isWebview {
				ww.SetURL(path)
			}
			w.Restore()
			w.Show()
			w.Focus()
			return
		}
	}
	if w := s.mainWindow(); w != nil {
		w.SetURL(path)
		w.Restore()
		w.Show()
		w.Focus()
	}
}

// OpenWindow opens path in a new app window, or focuses the window already
// showing it (clicking "Pop out" twice gives one pop-out). A tutorial's
// reading page opens in that tutorial's own window, one per tutorial.
func (s *shell) OpenWindow(path, title string) error {
	path, err := windowPath(path)
	if err != nil {
		return err
	}
	if title == "" {
		title = AppName
	}
	if slug, ok := tutorialPathSlug(path); ok && s.srv != nil {
		if _, _, known := s.srv.TutorialPage(path); known {
			s.openTutorialWindow(slug, path, title)
			return nil
		}
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

// openTutorialWindow shows path in the tutorial's own window, opening it
// (main-window sized, with its own agent panel) if it isn't open yet.
func (s *shell) openTutorialWindow(slug, path, title string) *application.WebviewWindow {
	name := tutorialWindowName(slug)
	if w, ok := s.app.Window.GetByName(name); ok {
		ww, isWebview := w.(*application.WebviewWindow)
		if isWebview {
			ww.SetURL(path)
		}
		w.Restore()
		w.Show()
		w.Focus()
		return ww
	}
	w := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:               name,
		Title:              title,
		URL:                path,
		Width:              mainWidth,
		Height:             mainHeight,
		MinWidth:           mainMinWidth,
		MinHeight:          mainMinHeight,
		BackgroundColour:   paper,
		Linux:              application.LinuxWindow{Icon: appIcon},
		UseApplicationMenu: true,
		EnableFileDrop:     true,
	})
	s.track(w, false, path)
	return w
}

// track records a window for the next launch, keeps its bounds current and
// routes folder drops on it to the page (#147).
func (s *shell) track(w *application.WebviewWindow, main bool, path string) {
	s.mu.Lock()
	if s.tracked == nil {
		s.tracked = map[string]*trackedWindow{}
	}
	tw := &trackedWindow{win: w, main: main, path: path}
	s.tracked[w.Name()] = tw
	s.mu.Unlock()

	// Window event listeners run on their own goroutines, so reading the
	// bounds (a main-thread call) is safe here, and nothing at quit time
	// has to touch a window that is going away.
	keep := func(*application.WindowEvent) {
		if w.IsMinimised() {
			return
		}
		b := w.Bounds()
		if b.Width == 0 || b.Height == 0 {
			return
		}
		s.mu.Lock()
		tw.bounds = rect{b.X, b.Y, b.Width, b.Height}
		s.mu.Unlock()
	}
	w.OnWindowEvent(events.Common.WindowDidMove, keep)
	w.OnWindowEvent(events.Common.WindowDidResize, keep)
	w.OnWindowEvent(events.Common.WindowShow, keep)
	if !main {
		w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
			s.mu.Lock()
			if !s.quitting {
				delete(s.tracked, w.Name())
			}
			s.mu.Unlock()
		})
	}
	w.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		ctx := e.Context()
		if ctx == nil || s.srv == nil {
			return
		}
		client := ""
		if d := ctx.DropTargetDetails(); d != nil {
			client = d.Attributes["data-drop-client"]
		}
		s.srv.ReportDrop(w.Name(), client, ctx.DroppedFiles())
	})
}

// saveWindows records the open windows, their pages and scroll positions in
// config.json for the next launch. The first call wins: it runs when the
// app starts to quit (the main window closing, Quit from the menu), before
// windows close, and again from OnShutdown in case neither ran. It only
// reads state kept current by events, so it is safe on any thread.
func (s *shell) saveWindows() {
	s.mu.Lock()
	s.quitting = true
	if s.saved || s.srv == nil {
		s.mu.Unlock()
		return
	}
	s.saved = true
	open := make([]openWindow, 0, len(s.tracked))
	for name, tw := range s.tracked {
		open = append(open, openWindow{name: name, main: tw.main, path: tw.path, bounds: tw.bounds})
	}
	s.mu.Unlock()

	states := snapshotWindows(open, s.srv.WindowViews())
	if err := config.UpdateConfig(func(c *config.Config) error {
		c.Windows = states
		return nil
	}); err != nil {
		s.logf("could not save the open windows for next time: %v", err)
	}
}

// restoreBounds puts restored windows back where they were, when that is
// still on a screen; others stay at their default place. Call it once the
// app has started (screens are known then), off the main thread.
func (s *shell) restoreBounds(plan []restoreWindow) {
	var screens []rect
	for _, sc := range s.app.Screen.GetAll() {
		if sc == nil {
			continue
		}
		wa := sc.WorkArea
		screens = append(screens, rect{wa.X, wa.Y, wa.Width, wa.Height})
	}
	for _, rw := range plan {
		if rw.bounds == (rect{}) || !onScreen(rw.bounds, screens) {
			continue
		}
		w, ok := s.app.Window.GetByName(rw.name)
		if !ok {
			continue
		}
		ww, ok := w.(*application.WebviewWindow)
		if !ok {
			continue
		}
		ww.SetBounds(application.Rect{X: rw.bounds.X, Y: rw.bounds.Y, Width: rw.bounds.W, Height: rw.bounds.H})
		s.mu.Lock()
		if tw := s.tracked[rw.name]; tw != nil {
			tw.bounds = rw.bounds
		}
		s.mu.Unlock()
	}
}
