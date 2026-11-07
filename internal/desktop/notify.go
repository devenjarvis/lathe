package desktop

import (
	"fmt"
	"sync"
	"time"
)

// notifyBackend is the platform side of a system notification. The Wails
// notifications service implements it in a desktop build; tests fake it.
type notifyBackend interface {
	// usable returns nil when notifications can be posted at all, or why
	// not (an unbundled macOS binary, no D-Bus session bus on Linux, ...).
	usable() error
	// authorize makes sure the app may post, asking the user if the OS
	// wants that (macOS). It may block until the user answers.
	authorize() (bool, error)
	send(id, title, body string) error
}

// notifier posts notifications without ever blocking or failing its caller:
// Notify only queues, and one goroutine authorizes (once) and sends in
// order. Whatever cannot be posted becomes a log line, so a finished run is
// still reported somewhere.
type notifier struct {
	backend notifyBackend
	logf    func(format string, args ...any)

	mu     sync.Mutex // guards closed and sends on queue
	closed bool
	queue  chan note
	done   chan struct{}

	// Owned by the run goroutine.
	authChecked bool
	authorized  bool
	seq         int
}

type note struct{ title, body string }

// notifyQueueSize bounds the backlog: a run ending is rare, so anything past
// this is a burst nobody wants to see as notifications anyway.
const notifyQueueSize = 16

// notifyCloseWait is how long close lets queued notifications drain.
var notifyCloseWait = 2 * time.Second

func newNotifier(b notifyBackend, logf func(string, ...any)) *notifier {
	n := &notifier{
		backend: b,
		logf:    logf,
		queue:   make(chan note, notifyQueueSize),
		done:    make(chan struct{}),
	}
	go n.run()
	return n
}

// Notify queues a notification. It never blocks.
func (n *notifier) Notify(title, body string) {
	if title == "" {
		title = AppName
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		n.logf("notification not posted (app is quitting): %s: %s", title, body)
		return
	}
	select {
	case n.queue <- note{title, body}:
	default:
		n.logf("notification dropped (queue full): %s: %s", title, body)
	}
}

// close stops the run goroutine after it posts what is already queued. It
// waits a moment for that, but not for an authorization prompt nobody is
// answering: quitting must not hang on a notification.
func (n *notifier) close() {
	n.mu.Lock()
	if !n.closed {
		n.closed = true
		close(n.queue)
	}
	n.mu.Unlock()
	select {
	case <-n.done:
	case <-time.After(notifyCloseWait):
	}
}

func (n *notifier) run() {
	defer close(n.done)
	for m := range n.queue {
		if err := n.post(m); err != nil {
			n.logf("notification not posted (%v): %s: %s", err, m.title, m.body)
		}
	}
}

func (n *notifier) post(m note) error {
	if err := n.backend.usable(); err != nil {
		return err
	}
	if !n.authChecked {
		ok, err := n.backend.authorize()
		if err != nil {
			// Not remembered: the user may answer the prompt next time.
			return fmt.Errorf("authorization: %w", err)
		}
		n.authChecked, n.authorized = true, ok
	}
	if !n.authorized {
		return fmt.Errorf("notifications are turned off for %s", AppName)
	}
	n.seq++
	return n.backend.send(fmt.Sprintf("lathe-%d", n.seq), m.title, m.body)
}
