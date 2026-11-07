//go:build desktop

package desktop

import (
	"context"
	"strconv"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
)

// badgeService is the Dock (macOS) or taskbar (Windows) badge: the count of
// things waiting on the reader (serve.Badger). Like notificationService it
// is a Wails service whose startup cannot fail: Wails' Linux dock is a stub
// (most desktops have no badges), and the Windows taskbar may refuse, so
// the badge is a no-op wherever it can't be shown. Its exported methods
// are only Wails' lifecycle hooks.
type badgeService struct {
	inner *dock.DockService
	logf  func(string, ...any)

	mu      sync.Mutex
	started bool
	shown   int
	warned  bool
}

func newBadgeService(logf func(string, ...any)) *badgeService {
	return &badgeService{inner: dock.New(), logf: logf}
}

// ServiceName names the service in Wails' logs.
func (b *badgeService) ServiceName() string { return "lathe-badge" }

// ServiceStartup starts the platform dock and keeps, rather than returns,
// its error.
func (b *badgeService) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	err := b.inner.ServiceStartup(ctx, opts)
	b.mu.Lock()
	b.started = err == nil
	b.mu.Unlock()
	if err != nil {
		b.logf("the app icon badge is off: %v", err)
	}
	return nil
}

// ServiceShutdown clears the badge and stops the platform dock.
func (b *badgeService) ServiceShutdown() error {
	b.mu.Lock()
	started := b.started
	b.started = false
	b.mu.Unlock()
	if !started {
		return nil
	}
	_ = b.inner.RemoveBadge()
	return b.inner.ServiceShutdown()
}

// set shows count on the app icon (0 clears it). It is called from the
// server's attention loop, never from the UI thread.
func (b *badgeService) set(count int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.started || count == b.shown {
		return
	}
	var err error
	if count <= 0 {
		err = b.inner.RemoveBadge()
	} else {
		label := strconv.Itoa(count)
		if count > 99 {
			label = "99+"
		}
		err = b.inner.SetBadge(label)
	}
	if err != nil {
		if !b.warned {
			b.logf("could not set the app icon badge: %v", err)
			b.warned = true
		}
		return
	}
	b.shown = count
}
