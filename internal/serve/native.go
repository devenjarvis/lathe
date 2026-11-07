package serve

// Native is what a desktop shell (the Wails app, internal/desktop) offers the
// reader on top of the browser: a native folder picker for the tutorial's
// workspace, a system notification when a Verify, Extend, Check or Ask run
// finishes, and real windows for the agent panel's pop-out. lathe serve in a
// browser has none of these, so every method is optional to use: a nil
// Native (the default) keeps today's browser behavior exactly.
//
// The page reaches these through same-origin POST endpoints, not through a
// JavaScript bridge, so the reader keeps one code path: the templates only
// learn whether a native shell is present (it changes which buttons render).
type Native interface {
	// PickFolder shows the OS folder picker, starting in start when it is
	// non-empty, and returns the chosen absolute path, or "" when the reader
	// cancels. It blocks until the dialog closes.
	PickFolder(title, start string) (string, error)
	// Notify posts a system notification. Best effort: it never blocks a job
	// and its errors are the shell's to log.
	Notify(title, body string)
	// OpenWindow opens path (a same-origin path such as
	// "/slug/part-01.md?agent=popout") in a new app window titled title.
	OpenWindow(path, title string) error
}

// SetNative installs the desktop shell's affordances. Call it before serving;
// nil removes them.
func (s *Server) SetNative(n Native) {
	s.nativeMu.Lock()
	s.native = n
	s.nativeMu.Unlock()
}

// nativeShell returns the installed shell, or nil in a plain browser.
func (s *Server) nativeShell() Native {
	s.nativeMu.RLock()
	defer s.nativeMu.RUnlock()
	return s.native
}
