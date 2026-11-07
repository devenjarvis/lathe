//go:build !desktop

package desktop

import "context"

// Available reports whether this binary was built with the desktop app
// (`go build -tags desktop`). This build was not.
func Available() bool { return false }

// Run would open the desktop app; this build has none, so it returns
// ErrNotBuilt.
func Run(context.Context, Options) error { return ErrNotBuilt }
