//go:build desktop

package desktop

import (
	"context"
	"errors"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// notificationService wraps Wails' notifications service as a Wails service
// whose startup cannot fail. Wails aborts the whole app when a service's
// startup errors, and the notifications service errors in ordinary
// situations: an unbundled macOS binary (no bundle identifier, so
// `go run`/dev builds), or Linux without a D-Bus session bus. Notifications
// are a nicety, so the error is kept and every notification turns into a
// log line instead (see notifier).
//
// It is the notifyBackend for the shell's notifier. Its exported methods are
// only Wails' lifecycle hooks, so the page gets no bound methods from it.
type notificationService struct {
	inner *notifications.NotificationService
	logf  func(string, ...any)

	mu      sync.Mutex
	err     error
	started bool
}

func newNotificationService(logf func(string, ...any)) *notificationService {
	return &notificationService{
		inner: notifications.New(),
		logf:  logf,
		err:   errors.New("the app has not started"),
	}
}

// ServiceName names the service in Wails' logs.
func (s *notificationService) ServiceName() string { return "lathe-notifications" }

// ServiceStartup starts the platform notifier and keeps, rather than
// returns, its error.
func (s *notificationService) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	err := s.inner.ServiceStartup(ctx, opts)
	s.mu.Lock()
	s.err, s.started = err, err == nil
	s.mu.Unlock()
	if err != nil {
		s.logf("system notifications are off: %v", err)
	}
	return nil
}

// ServiceShutdown stops the platform notifier if it started.
func (s *notificationService) ServiceShutdown() error {
	s.mu.Lock()
	started := s.started
	s.started = false
	s.err = errors.New("the app is quitting")
	s.mu.Unlock()
	if started {
		return s.inner.ServiceShutdown()
	}
	return nil
}

func (s *notificationService) usable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// authorize checks for permission and asks for it when it is missing. Only
// macOS asks (once, the first time the app posts, not at launch); Linux and
// Windows always allow.
func (s *notificationService) authorize() (bool, error) {
	ok, err := s.inner.CheckNotificationAuthorization()
	if err == nil && ok {
		return true, nil
	}
	return s.inner.RequestNotificationAuthorization()
}

func (s *notificationService) send(id, title, body string) error {
	return s.inner.SendNotification(notifications.NotificationOptions{
		ID:    id,
		Title: title,
		Body:  body,
	})
}
