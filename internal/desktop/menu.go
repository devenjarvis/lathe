//go:build desktop

package desktop

import (
	"runtime"
	"sync"

	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/devenjarvis/lathe/internal/shortcuts"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// menuBar is the app's menu bar, rendered from menuPlan. Page-run commands
// (shortcuts.HandlerPage) go to the focused page through the server
// (serve.Server.RunCommand); shell commands are handled here; roles are the
// toolkit's.
type menuBar struct {
	app  *application.App
	sh   *shell
	srv  *serve.Server
	menu *application.Menu
	// page holds the items of page-run commands, which enable and disable
	// with the focused page.
	page map[string]*application.MenuItem

	// The latest state from the server, applied off the request goroutine
	// (setState never blocks a handler on the UI thread).
	mu      sync.Mutex
	pending *serve.MenuState
	kick    chan struct{}
}

var roleOf = map[string]application.Role{
	roleQuit:       application.Quit,
	roleClose:      application.CloseWindow,
	roleServices:   application.ServicesMenu,
	roleHide:       application.Hide,
	roleHideApps:   application.HideOthers,
	roleShowAll:    application.UnHide,
	roleMinimise:   application.Minimise,
	roleZoom:       application.Zoom,
	roleEditMenu:   application.EditMenu,
	roleWindowMenu: application.WindowMenu,
}

// newMenuBar builds the menu and installs it as the application menu. Call
// it before creating windows: on Windows and Linux a window takes the
// application menu when it is created (UseApplicationMenu).
func newMenuBar(app *application.App, sh *shell, srv *serve.Server) *menuBar {
	mb := &menuBar{
		app:  app,
		sh:   sh,
		srv:  srv,
		menu: application.NewMenu(),
		page: map[string]*application.MenuItem{},
		kick: make(chan struct{}, 1),
	}
	for _, sec := range menuPlan(runtime.GOOS) {
		if sec.role != "" {
			mb.menu.AddRole(roleOf[sec.role])
			continue
		}
		mb.addEntries(mb.menu.AddSubmenu(sec.title), sec.entries)
	}
	app.Menu.Set(mb.menu)
	go mb.applyLoop()
	return mb
}

func (mb *menuBar) addEntries(m *application.Menu, entries []menuEntry) {
	for _, e := range entries {
		switch {
		case e.sep:
			m.AddSeparator()
		case e.role != "":
			m.AddRole(roleOf[e.role])
		case e.submenu != "":
			mb.addEntries(m.AddSubmenu(e.submenu), e.children)
		default:
			mb.addCommand(m, e)
		}
	}
}

func (mb *menuBar) addCommand(m *application.Menu, e menuEntry) {
	sc := e.cmd
	item := m.Add(e.label)
	if e.accel != "" {
		item.SetAccelerator(e.accel)
	}
	switch {
	case sc.Stub:
		// Not built yet: listed so the menu shows what is coming.
		item.SetEnabled(false)
		return
	case sc.Handler == shortcuts.HandlerPage:
		// Off until the focused page says it can run it.
		item.SetEnabled(false)
		mb.page[sc.ID] = item
	}
	item.OnClick(func(*application.Context) { mb.run(sc) })
}

// run carries out a click (or an accelerator; Windows fires those even for a
// disabled item, so nothing here trusts the item's state).
func (mb *menuBar) run(sc shortcuts.Shortcut) {
	switch sc.Handler {
	case shortcuts.HandlerShell:
		switch sc.ID {
		case "app.about":
			mb.app.Menu.ShowAbout()
		case "app.updates":
			if u := mb.sh.updates; u != nil {
				// It waits on the network: not on the UI thread.
				go u.checkNow()
			}
		case "app.settings", "agent.settings":
			if err := mb.sh.openSettings(sc.Href); err != nil {
				mb.sh.logf("could not open Settings: %v", err)
			}
		}
	case shortcuts.HandlerPage:
		if mb.srv.RunCommand(sc.ID) {
			return
		}
		// No page is listening (it failed to load, or is still loading):
		// a navigation can still go to the main window.
		if sc.Href != "" && mb.srv.MenuState().Path == "" {
			if w := mb.sh.mainWindow(); w != nil {
				w.SetURL(sc.Href)
			}
		}
	}
}

// setState records the focused page's state and wakes the apply loop.
func (mb *menuBar) setState(st serve.MenuState) {
	mb.mu.Lock()
	mb.pending = &st
	mb.mu.Unlock()
	select {
	case mb.kick <- struct{}{}:
	default:
	}
}

// applyLoop applies the latest state; states that arrive while it works
// collapse into one.
func (mb *menuBar) applyLoop() {
	for range mb.kick {
		mb.mu.Lock()
		st := mb.pending
		mb.pending = nil
		mb.mu.Unlock()
		if st == nil {
			continue
		}
		for id, item := range mb.page {
			if on := st.Enabled[id]; on != item.Enabled() {
				item.SetEnabled(on)
			}
		}
	}
}
