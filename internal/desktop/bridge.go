package desktop

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
)

// bridge is the app's CLI-facing listener. The window never uses it (it loads
// the reader through the Wails asset server), but `lathe work`, `lathe start`
// and `lathe verify-cancel` find the running server through
// ~/.lathe/serve.json, so the app serves the same Server there on an
// ephemeral loopback port and records that URL while it runs.
type bridge struct {
	URL string

	srv  *http.Server
	pid  int
	logf func(string, ...any)
	once sync.Once
}

// bridgeShutdownWait bounds the graceful part of close: the worker's
// long-poll (GET /-/work) and the agent panel's SSE stream hold connections
// open for up to a minute, and quitting must not wait for them.
const bridgeShutdownWait = 2 * time.Second

// startBridge listens on 127.0.0.1:0 (loopback only: the server is
// unauthenticated and can delete tutorials, so it must never be reachable from
// other devices), serves h there and writes serve.json. A failed serve.json
// write is logged, not fatal: it only means the CLI can't find the app.
func startBridge(h http.Handler, logf func(string, ...any)) (*bridge, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := &bridge{
		URL:  "http://" + ln.Addr().String(),
		srv:  &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second},
		pid:  os.Getpid(),
		logf: logf,
	}
	go func() {
		if err := b.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf("CLI listener stopped: %v", err)
		}
	}()
	rt := &config.ServeRuntime{URL: b.URL, PID: b.pid, Started: time.Now().UTC()}
	if err := config.WriteServeRuntime(rt); err != nil {
		logf("could not write the serve runtime file, so `lathe work` won't find the app: %v", err)
	}
	return b, nil
}

// close stops the listener and removes serve.json. It is safe to call more
// than once (the app calls it from its shutdown hook and again after Run
// returns, because on macOS Run never returns).
func (b *bridge) close() {
	b.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), bridgeShutdownWait)
		defer cancel()
		if err := b.srv.Shutdown(ctx); err != nil {
			_ = b.srv.Close()
		}
		if err := removeOwnServeRuntime(b.pid); err != nil {
			b.logf("could not remove the serve runtime file: %v", err)
		}
	})
}

// removeOwnServeRuntime removes serve.json only while it still names this
// process: a `lathe serve` started after the app owns the file now, and
// removing it would hide that server from the CLI.
func removeOwnServeRuntime(pid int) error {
	rt, err := config.ReadServeRuntime()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		// Unreadable: it can't be pointing anywhere useful.
		return config.RemoveServeRuntime()
	}
	if rt.PID != pid {
		return nil
	}
	return config.RemoveServeRuntime()
}
