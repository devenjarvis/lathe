package desktop

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestHasDisplay(t *testing.T) {
	for _, tc := range []struct {
		goos string
		env  map[string]string
		want bool
	}{
		{"darwin", nil, true},
		{"windows", nil, true},
		{"linux", nil, false},
		{"linux", map[string]string{"DISPLAY": ":0"}, true},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, true},
		{"freebsd", map[string]string{"DISPLAY": ":1"}, true},
		{"freebsd", nil, false},
	} {
		got, why := hasDisplay(tc.goos, env(tc.env))
		if got != tc.want {
			t.Errorf("hasDisplay(%s, %v) = %v, want %v", tc.goos, tc.env, got, tc.want)
		}
		if !got && !strings.Contains(why, "DISPLAY") {
			t.Errorf("reason %q should name the variables", why)
		}
	}
}

func TestWindowPath(t *testing.T) {
	ok := []string{"/", "/slug/part-01.md?agent=popout", "/slug/"}
	for _, p := range ok {
		if _, err := windowPath(p); err != nil {
			t.Errorf("windowPath(%q): %v", p, err)
		}
	}
	bad := []string{"", "slug/", "//evil.example/x", "https://evil.example/", `/\evil.example`, "/a\nb", "/a\x00"}
	for _, p := range bad {
		if _, err := windowPath(p); err == nil {
			t.Errorf("windowPath(%q) accepted", p)
		}
	}
}

// fakeBackend is a scripted notifyBackend.
type fakeBackend struct {
	mu        sync.Mutex
	unusable  error
	authOK    bool
	authErr   error
	authCalls int
	block     chan struct{} // if set, authorize waits on it
	sent      []string
}

func (f *fakeBackend) usable() error { return f.unusable }

func (f *fakeBackend) authorize() (bool, error) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authCalls++
	return f.authOK, f.authErr
}

func (f *fakeBackend) send(id, title, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, id+"|"+title+"|"+body)
	return nil
}

type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logLines) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func TestNotifierSendsInOrderAndAuthorizesOnce(t *testing.T) {
	b := &fakeBackend{authOK: true}
	var log logLines
	n := newNotifier(b, log.logf)
	n.Notify("Verified", "demo passed")
	n.Notify("", "no title")
	n.close()

	want := []string{"lathe-1|Verified|demo passed", "lathe-2|Lathe|no title"}
	if strings.Join(b.sent, ",") != strings.Join(want, ",") {
		t.Errorf("sent %v, want %v", b.sent, want)
	}
	if b.authCalls != 1 {
		t.Errorf("authorize called %d times, want 1", b.authCalls)
	}
	if log.all() != "" {
		t.Errorf("unexpected log: %s", log.all())
	}
}

func TestNotifierDegradesToLogLine(t *testing.T) {
	b := &fakeBackend{unusable: errors.New("notifications require a valid bundle identifier")}
	var log logLines
	n := newNotifier(b, log.logf)
	n.Notify("Verified", "demo passed")
	n.close()
	if len(b.sent) != 0 {
		t.Errorf("sent %v on an unusable backend", b.sent)
	}
	if got := log.all(); !strings.Contains(got, "bundle identifier") || !strings.Contains(got, "demo passed") {
		t.Errorf("log = %q, want the reason and the message", got)
	}
}

func TestNotifierDeniedAndRetriesAfterError(t *testing.T) {
	b := &fakeBackend{authErr: errors.New("timed out")}
	var log logLines
	n := newNotifier(b, log.logf)
	n.Notify("a", "1")
	n.Notify("b", "2")
	n.close()
	if b.authCalls != 2 {
		t.Errorf("an authorization error should be retried next time; calls = %d", b.authCalls)
	}

	b = &fakeBackend{authOK: false}
	log = logLines{}
	n = newNotifier(b, log.logf)
	n.Notify("a", "1")
	n.Notify("b", "2")
	n.close()
	if b.authCalls != 1 || len(b.sent) != 0 || !strings.Contains(log.all(), "turned off") {
		t.Errorf("denied: calls=%d sent=%v log=%q", b.authCalls, b.sent, log.all())
	}
}

func TestNotifyNeverBlocks(t *testing.T) {
	b := &fakeBackend{authOK: true, block: make(chan struct{})}
	var log logLines
	n := newNotifier(b, log.logf)
	done := make(chan struct{})
	go func() {
		for i := 0; i < notifyQueueSize*3; i++ {
			n.Notify("t", "b")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Notify blocked behind a pending authorization")
	}
	if !strings.Contains(log.all(), "queue full") {
		t.Error("overflow should be logged")
	}

	// close doesn't hang on an authorization nobody answers.
	old := notifyCloseWait
	notifyCloseWait = 50 * time.Millisecond
	defer func() { notifyCloseWait = old }()
	start := time.Now()
	n.close()
	if time.Since(start) > time.Second {
		t.Error("close waited on the pending authorization")
	}
	n.Notify("late", "after close") // must not panic
	close(b.block)
}

func TestBridgeWritesAndRemovesServeRuntime(t *testing.T) {
	setHome(t, t.TempDir())
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "lathe") })
	var log logLines
	b, err := startBridge(h, log.logf)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := config.ReadServeRuntime()
	if err != nil {
		t.Fatalf("serve.json: %v", err)
	}
	if rt.URL != b.URL || rt.PID != os.Getpid() || !strings.HasPrefix(rt.URL, "http://127.0.0.1:") {
		t.Errorf("serve.json = %+v, bridge URL %s", rt, b.URL)
	}
	resp, err := http.Get(rt.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "lathe" {
		t.Errorf("body = %q", body)
	}

	b.close()
	b.close() // idempotent
	if _, err := config.ReadServeRuntime(); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("serve.json should be gone, err = %v", err)
	}
	if _, err := http.Get(rt.URL + "/"); err == nil {
		t.Error("listener still serving after close")
	}
	if log.all() != "" {
		t.Errorf("unexpected log: %s", log.all())
	}
}

func TestBridgeKeepsAnotherServersRuntime(t *testing.T) {
	setHome(t, t.TempDir())
	var log logLines
	b, err := startBridge(http.NotFoundHandler(), log.logf)
	if err != nil {
		t.Fatal(err)
	}
	// A `lathe serve` started later took the file over.
	other := &config.ServeRuntime{URL: "http://localhost:4242", PID: os.Getpid() + 1}
	if err := config.WriteServeRuntime(other); err != nil {
		t.Fatal(err)
	}
	b.close()
	rt, err := config.ReadServeRuntime()
	if err != nil || rt.URL != other.URL {
		t.Errorf("the other server's serve.json was removed: %+v, %v", rt, err)
	}
}

func TestStubOrAvailable(t *testing.T) {
	if !Available() {
		if err := Run(t.Context(), Options{}); !errors.Is(err, ErrNotBuilt) {
			t.Errorf("stub Run = %v, want ErrNotBuilt", err)
		}
	}
}
