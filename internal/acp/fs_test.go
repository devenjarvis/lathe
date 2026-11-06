package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/acp/acptest"
)

// activityRecorder is a recorder that also collects Activity reports.
type activityRecorder struct {
	recorder
	amu  sync.Mutex
	acts []acp.Activity
}

func (r *activityRecorder) Activity(a acp.Activity) {
	r.amu.Lock()
	defer r.amu.Unlock()
	r.acts = append(r.acts, a)
}

func (r *activityRecorder) activities() []acp.Activity {
	r.amu.Lock()
	defer r.amu.Unlock()
	return append([]acp.Activity(nil), r.acts...)
}

func call(t *testing.T, c *acp.Client, method string, params any) (map[string]any, error) {
	t.Helper()
	raw, _ := json.Marshal(params)
	res, err := c.HandleRequest(context.Background(), method, raw)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out, nil
}

func wantRefused(t *testing.T, err error, what string) {
	t.Helper()
	var rpc *acp.RPCError
	if !errors.As(err, &rpc) || rpc.Code != -32602 || !strings.Contains(rpc.Message, "outside this job's file policy") {
		t.Errorf("%s: err = %v, want a policy refusal", what, err)
	}
}

func dirs(t *testing.T) (tut, scratch string) {
	t.Helper()
	root := t.TempDir()
	tut, scratch = filepath.Join(root, "tut"), filepath.Join(root, "scratch")
	for _, d := range []string{tut, scratch} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tut, "part-01.md"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return tut, scratch
}

func TestInitializeAdvertisesFsAndTerminal(t *testing.T) {
	r, w := io.Pipe()
	sent := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := r.Read(buf)
		sent <- buf[:n]
	}()
	idle, idleW := io.Pipe() // stays open, so the connection does too
	t.Cleanup(func() { _ = idleW.Close() })
	c := acp.NewClient(idle, w, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _, _ = c.Initialize(ctx) }()
	var req struct {
		Params struct {
			Caps struct {
				Fs struct {
					Read  bool `json:"readTextFile"`
					Write bool `json:"writeTextFile"`
				} `json:"fs"`
				Terminal bool `json:"terminal"`
			} `json:"clientCapabilities"`
		} `json:"params"`
	}
	select {
	case raw := <-sent:
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
	case <-ctx.Done():
		t.Fatal("no initialize sent")
	}
	if c := req.Params.Caps; !c.Fs.Read || !c.Fs.Write || !c.Terminal {
		t.Errorf("capabilities = %+v, want fs read/write and terminal", c)
	}
}

func TestFsWithinPolicy(t *testing.T) {
	tut, scratch := dirs(t)
	c := acp.NewClient(strings.NewReader(""), io.Discard, nil)

	// No policy for the session yet: everything is refused.
	_, err := call(t, c, "fs/read_text_file", map[string]any{"sessionId": "s", "path": filepath.Join(tut, "part-01.md")})
	wantRefused(t, err, "read without a policy")

	c.SetPolicy("s", acp.VerifyPolicy(tut, scratch))
	out, err := call(t, c, "fs/read_text_file", map[string]any{"sessionId": "s", "path": filepath.Join(tut, "part-01.md"), "line": 2, "limit": 1})
	if err != nil || out["content"] != "two\n" {
		t.Fatalf("read line 2 = %v, %v", out, err)
	}

	target := filepath.Join(scratch, "src", "main.go")
	if _, err := call(t, c, "fs/write_text_file", map[string]any{"sessionId": "s", "path": target, "content": "package main\n"}); err != nil {
		t.Fatalf("write in scratch: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "package main\n" {
		t.Errorf("scratch file = %q", b)
	}

	// Out-of-policy writes are refused and leave the disk untouched.
	tutFile := filepath.Join(tut, "part-01.md")
	_, err = call(t, c, "fs/write_text_file", map[string]any{"sessionId": "s", "path": tutFile, "content": "clobbered"})
	wantRefused(t, err, "write into the tutorial during verify")
	if b, _ := os.ReadFile(tutFile); string(b) != "one\ntwo\nthree\n" {
		t.Errorf("tutorial file changed: %q", b)
	}
	home := filepath.Join(filepath.Dir(tut), "elsewhere.txt")
	_, err = call(t, c, "fs/write_text_file", map[string]any{"sessionId": "s", "path": home, "content": "x"})
	wantRefused(t, err, "write outside every root")
	if _, serr := os.Stat(home); serr == nil {
		t.Error("refused write still created the file")
	}

	// Another session's policy does not leak.
	_, err = call(t, c, "fs/read_text_file", map[string]any{"sessionId": "other", "path": tutFile})
	wantRefused(t, err, "read from a session with no policy")
}

func TestTerminalWithinPolicy(t *testing.T) {
	tut, scratch := dirs(t)
	c := acp.NewClient(strings.NewReader(""), io.Discard, nil)
	t.Cleanup(func() { _ = c.Close() })
	c.SetPolicy("s", acp.VerifyPolicy(tut, scratch))

	out, err := call(t, c, "terminal/create", map[string]any{"sessionId": "s", "command": "pwd && echo hi && exit 3"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := out["terminalId"]
	ref := map[string]any{"sessionId": "s", "terminalId": id}
	st, err := call(t, c, "terminal/wait_for_exit", ref)
	if err != nil || st["exitCode"] != float64(3) {
		t.Fatalf("wait = %v, %v; want exit 3", st, err)
	}
	o, err := call(t, c, "terminal/output", ref)
	if err != nil {
		t.Fatal(err)
	}
	realScratch, _ := filepath.EvalSymlinks(scratch)
	if got := o["output"].(string); !strings.Contains(got, realScratch) || !strings.Contains(got, "hi") {
		t.Errorf("output = %q, want the scratch cwd and hi", got)
	}
	if o["exitStatus"] == nil {
		t.Error("output after exit should carry exitStatus")
	}
	// Another session cannot reach this terminal.
	if _, err := call(t, c, "terminal/output", map[string]any{"sessionId": "other", "terminalId": id}); err == nil {
		t.Error("terminal visible from another session")
	}
	if _, err := call(t, c, "terminal/release", ref); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, c, "terminal/output", ref); err == nil {
		t.Error("released terminal still readable")
	}

	// Commands outside the exec roots are refused before anything starts.
	marker := filepath.Join(tut, "ran")
	_, err = call(t, c, "terminal/create", map[string]any{"sessionId": "s", "command": "touch ran", "cwd": tut})
	wantRefused(t, err, "run in the tutorial dir")
	if _, serr := os.Stat(marker); serr == nil {
		t.Error("refused command ran anyway")
	}
	c.SetPolicy("ask", acp.AskPolicy(tut))
	_, err = call(t, c, "terminal/create", map[string]any{"sessionId": "ask", "command": "true"})
	wantRefused(t, err, "run anything during ask")
}

func TestTerminalKillAndTruncate(t *testing.T) {
	tut, scratch := dirs(t)
	c := acp.NewClient(strings.NewReader(""), io.Discard, nil)
	t.Cleanup(func() { _ = c.Close() })
	c.SetPolicy("s", acp.VerifyPolicy(tut, scratch))

	out, err := call(t, c, "terminal/create", map[string]any{"sessionId": "s", "command": "printf 0123456789", "outputByteLimit": 4})
	if err != nil {
		t.Fatal(err)
	}
	ref := map[string]any{"sessionId": "s", "terminalId": out["terminalId"]}
	if _, err := call(t, c, "terminal/wait_for_exit", ref); err != nil {
		t.Fatal(err)
	}
	o, _ := call(t, c, "terminal/output", ref)
	if o["output"] != "6789" || o["truncated"] != true {
		t.Errorf("truncated output = %v", o)
	}

	out, err = call(t, c, "terminal/create", map[string]any{"sessionId": "s", "command": "sleep", "args": []string{"30"}})
	if err != nil {
		t.Fatal(err)
	}
	ref = map[string]any{"sessionId": "s", "terminalId": out["terminalId"]}
	if _, err := call(t, c, "terminal/kill", ref); err != nil {
		t.Fatal(err)
	}
	done := make(chan map[string]any, 1)
	go func() { st, _ := call(t, c, "terminal/wait_for_exit", ref); done <- st }()
	select {
	case st := <-done:
		if st["exitCode"] != nil || st["signal"] == nil {
			t.Errorf("killed status = %v, want a signal", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kill did not stop the command")
	}
}

func TestAgentRoutesThroughPolicy(t *testing.T) {
	tut, _ := dirs(t)
	a := &acptest.Agent{Reply: func(string) string { return "done" }}
	c, _ := pipeClient(t, a)
	if _, err := c.Initialize(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if got := c.Isolation().State; got != "unknown" {
		t.Errorf("isolation before any file access = %q", got)
	}
	sid, err := c.NewSession(ctx(t), tut)
	if err != nil {
		t.Fatal(err)
	}
	c.SetPolicy(sid, acp.AskPolicy(tut))
	rec := &activityRecorder{}
	if _, err := c.Prompt(ctx(t), sid, "[read:part-01.md] [write:notes.md] [run:ls]", rec); err != nil {
		t.Fatal(err)
	}
	text := rec.String()
	for _, want := range []string{"(read ok: 14 bytes)", "(write refused:", "(run refused:"} {
		if !strings.Contains(text, want) {
			t.Errorf("reply %q lacks %q", text, want)
		}
	}
	if _, err := os.Stat(filepath.Join(tut, "notes.md")); err == nil {
		t.Error("ask policy let the agent write into the tutorial")
	}
	acts := rec.activities()
	if len(acts) != 3 || acts[0].Kind != "read" || acts[0].Denied || !acts[1].Denied || !acts[2].Denied || acts[2].Kind != "terminal" {
		t.Errorf("activities = %+v", acts)
	}
	if got := c.Isolation().State; got != "enforced" {
		t.Errorf("isolation = %q, want enforced", got)
	}
}

func TestOwnToolsMeanSoftIsolation(t *testing.T) {
	a := &acptest.Agent{Reply: func(string) string { return "done" }}
	c, _ := pipeClient(t, a)
	if _, err := c.Initialize(ctx(t)); err != nil {
		t.Fatal(err)
	}
	sid, _ := c.NewSession(ctx(t), t.TempDir())
	if _, err := c.Prompt(ctx(t), sid, "[owntool]", &recorder{}); err != nil {
		t.Fatal(err)
	}
	iso := c.Isolation()
	if iso.State != "soft" || iso.Detail == "" {
		t.Errorf("isolation = %+v, want soft", iso)
	}
}
