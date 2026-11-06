package acp_test

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/acp/acptest"
)

// TestMain doubles as a fake agent process: with LATHE_ACP_FAKE_AGENT set,
// the test binary serves ACP on stdio instead of running tests, so Start can
// be exercised against a real child process.
func TestMain(m *testing.M) {
	if os.Getenv("LATHE_ACP_FAKE_AGENT") == "1" {
		a := &acptest.Agent{}
		if err := a.Serve(os.Stdin, os.Stdout); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// recorder is a SessionHandler that collects chunks and answers permission
// requests with a fixed choice.
type recorder struct {
	mu      sync.Mutex
	text    strings.Builder
	asked   []acp.PermissionRequest
	choose  string // "" → cancelled
	updates int
}

func (r *recorder) Update(u acp.SessionUpdate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates++
	if u.Kind == "agent_message_chunk" {
		r.text.WriteString(u.Text())
	}
}

func (r *recorder) RequestPermission(ctx context.Context, req acp.PermissionRequest) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, req)
	return r.choose, r.choose != ""
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.text.String()
}

// pipeClient wires a Client to an in-process fake agent.
func pipeClient(t *testing.T, a *acptest.Agent) (*acp.Client, chan error) {
	t.Helper()
	toAgentR, toAgentW := io.Pipe()
	fromAgentR, fromAgentW := io.Pipe()
	served := make(chan error, 1)
	go func() {
		err := a.Serve(toAgentR, fromAgentW)
		_ = fromAgentW.Close()
		served <- err
	}()
	c := acp.NewClient(fromAgentR, toAgentW, toAgentW.Close)
	t.Cleanup(func() { _ = c.Close() })
	return c, served
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestPromptStreamsChunks(t *testing.T) {
	a := &acptest.Agent{Reply: func(string) string { return "The modulo wraps the phase." }}
	c, _ := pipeClient(t, a)
	if _, err := c.Initialize(ctx(t)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid, err := c.NewSession(ctx(t), "/tmp/tut")
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	rec := &recorder{}
	stop, err := c.Prompt(ctx(t), sid, "why modulo?", rec)
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if stop != "end_turn" {
		t.Errorf("stopReason = %q, want end_turn", stop)
	}
	if got := rec.String(); got != "The modulo wraps the phase." {
		t.Errorf("streamed text = %q", got)
	}
	if rec.updates < 2 {
		t.Errorf("want the answer in several chunks, got %d updates", rec.updates)
	}
	if len(a.Cwds) != 1 || a.Cwds[0] != "/tmp/tut" {
		t.Errorf("session cwd = %v", a.Cwds)
	}
}

func TestPermissionSelectedAndCancelled(t *testing.T) {
	for _, tc := range []struct {
		choose, want string
	}{
		{"allow", "(permission granted) "},
		{"deny", "(permission denied) "},
		{"", ""}, // cancelled: the agent ends the turn without answering
	} {
		t.Run(tc.choose, func(t *testing.T) {
			a := &acptest.Agent{Reply: func(string) string { return "ok" }}
			c, _ := pipeClient(t, a)
			if _, err := c.Initialize(ctx(t)); err != nil {
				t.Fatal(err)
			}
			sid, _ := c.NewSession(ctx(t), "/tmp")
			rec := &recorder{choose: tc.choose}
			stop, err := c.Prompt(ctx(t), sid, "[permission] read it", rec)
			if err != nil {
				t.Fatal(err)
			}
			if len(rec.asked) != 1 || len(rec.asked[0].Options) != 2 || rec.asked[0].ToolCall.Title == "" {
				t.Fatalf("permission request = %+v", rec.asked)
			}
			if tc.choose == "" {
				if stop != "cancelled" {
					t.Errorf("stopReason = %q, want cancelled", stop)
				}
				return
			}
			if got := rec.String(); !strings.HasPrefix(got, tc.want) {
				t.Errorf("text = %q, want prefix %q", got, tc.want)
			}
		})
	}
}

func TestPromptCancelSendsSessionCancel(t *testing.T) {
	a := &acptest.Agent{Reply: func(string) string { return strings.Repeat("word ", 200) }}
	c, _ := pipeClient(t, a)
	if _, err := c.Initialize(ctx(t)); err != nil {
		t.Fatal(err)
	}
	sid, _ := c.NewSession(ctx(t), "/tmp")
	pctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	stop, err := c.Prompt(pctx, sid, "[slow] go", &recorder{})
	if stop != "cancelled" {
		t.Errorf("stopReason = %q, want cancelled", stop)
	}
	if err != nil {
		t.Errorf("a cancel the agent acknowledged should not be an error, got %v", err)
	}
}

func TestCrashClosesConnection(t *testing.T) {
	a := &acptest.Agent{}
	c, served := pipeClient(t, a)
	if _, err := c.Initialize(ctx(t)); err != nil {
		t.Fatal(err)
	}
	sid, _ := c.NewSession(ctx(t), "/tmp")
	_, err := c.Prompt(ctx(t), sid, "[crash]", &recorder{})
	if !errors.Is(err, acp.ErrClosed) {
		t.Fatalf("prompt err = %v, want ErrClosed", err)
	}
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed after crash")
	}
	if !errors.Is(<-served, acptest.ErrCrash) {
		t.Error("fake agent did not report the crash")
	}
	if _, err := c.NewSession(ctx(t), "/tmp"); !errors.Is(err, acp.ErrClosed) {
		t.Errorf("call after close = %v, want ErrClosed", err)
	}
}

func TestStartRealProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	t.Setenv("LATHE_ACP_FAKE_AGENT", "1")
	c, err := acp.Start(ctx(t), "'"+exe+"'", t.TempDir(), io.Discard)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = c.Close() }()
	sid, err := c.NewSession(ctx(t), "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	if _, err := c.Prompt(ctx(t), sid, "hello", rec); err != nil {
		t.Fatal(err)
	}
	if rec.String() == "" {
		t.Error("no text streamed from the child process")
	}

	// A crash in the child closes the connection.
	if _, err := c.Prompt(ctx(t), sid, "[crash]", &recorder{}); !errors.Is(err, acp.ErrClosed) {
		t.Errorf("crash err = %v, want ErrClosed", err)
	}
}

func TestStartBadCommand(t *testing.T) {
	_, err := acp.Start(ctx(t), "exit 7", t.TempDir(), io.Discard)
	if err == nil {
		t.Fatal("want an error when the agent exits before initialize")
	}
}

func TestUnsupportedRequestRefused(t *testing.T) {
	c := acp.NewClient(strings.NewReader(""), io.Discard, nil)
	_, err := c.HandleRequest(context.Background(), "fs/read_text_file", nil)
	var rpcErr *acp.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		t.Errorf("err = %v, want method-not-found", err)
	}
}
