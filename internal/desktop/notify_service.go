//go:build desktop

package desktop

import (
	"context"
	"errors"
	"strings"
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
	// categories are the action categories registered so far, by label.
	categories map[string]string
	// onOpen is told the page to show when the reader clicks a
	// notification or its button (see sendAction).
	onOpen func(path string)
}

func newNotificationService(logf func(string, ...any)) *notificationService {
	s := &notificationService{
		inner:      notifications.New(),
		logf:       logf,
		err:        errors.New("the app has not started"),
		categories: map[string]string{},
	}
	s.inner.OnNotificationResponse(s.response)
	return s
}

// setOnOpen sets what a click on an actionable notification does.
func (s *notificationService) setOnOpen(fn func(path string)) {
	s.mu.Lock()
	s.onOpen = fn
	s.mu.Unlock()
}

// notifyActionID is the one action button's id in every category.
const notifyActionID = "open"

// response handles a click on a notification: on its action button, or on
// the notification itself, which does the same. Plain notifications carry
// no path, so a click on one only brings the app forward (the OS does that).
func (s *notificationService) response(r notifications.NotificationResult) {
	if r.Error != nil {
		s.logf("notification response: %v", r.Error)
		return
	}
	switch r.Response.ActionIdentifier {
	case notifyActionID, notifications.DefaultActionIdentifier:
	default:
		return
	}
	path, _ := r.Response.UserInfo["path"].(string)
	s.mu.Lock()
	fn := s.onOpen
	s.mu.Unlock()
	if path != "" && fn != nil {
		go fn(path)
	}
}

// category returns the action category for label, registering it the first
// time ("Open", "Answer", "View report" each get one with a single button).
func (s *notificationService) category(label string) (string, error) {
	s.mu.Lock()
	id, ok := s.categories[label]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	id = "lathe-" + strings.ToLower(strings.ReplaceAll(label, " ", "-"))
	err := s.inner.RegisterNotificationCategory(notifications.NotificationCategory{
		ID:      id,
		Actions: []notifications.NotificationAction{{ID: notifyActionID, Title: label}},
	})
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.categories[label] = id
	s.mu.Unlock()
	return id, nil
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

// sendAction posts a notification with one button, label, that opens path
// (as does a click on the notification). Every platform Wails supports can
// show it (macOS categories, Windows toast buttons, freedesktop actions); a
// Linux notification server without actions just shows no button.
func (s *notificationService) sendAction(id, title, body, label, path string) error {
	cat, err := s.category(label)
	if err != nil {
		return err
	}
	return s.inner.SendNotificationWithActions(notifications.NotificationOptions{
		ID:         id,
		Title:      title,
		Body:       body,
		CategoryID: cat,
		Data:       map[string]interface{}{"path": path},
	})
}
