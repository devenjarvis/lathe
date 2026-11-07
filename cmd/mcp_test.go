package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/mcp"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/voice"
)

// mcpClient talks to an in-process Lathe MCP server over pipes, the way an
// agent talks to `lathe mcp` over stdio.
type mcpClient struct {
	t    *testing.T
	conn *acp.Conn
}

func newMCPClient(t *testing.T, slug string) *mcpClient {
	t.Helper()
	return newMCPServerClient(t, &mcp.Server{Slug: slug, Version: "test"})
}

func newMCPServerClient(t *testing.T, srv *mcp.Server) *mcpClient {
	t.Helper()
	toSrvR, toSrvW := io.Pipe()
	fromSrvR, fromSrvW := io.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- srv.Serve(toSrvR, fromSrvW)
		_ = fromSrvW.Close()
	}()
	c := &mcpClient{t: t, conn: acp.NewConn(fromSrvR, toSrvW, nil)}
	t.Cleanup(func() {
		_ = toSrvW.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	c.rpc("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "test", "version": "0"},
	}, &init)
	if init.ProtocolVersion != "2025-06-18" || init.ServerInfo.Name != "lathe" {
		t.Fatalf("initialize = %+v", init)
	}
	if err := c.conn.Notify("notifications/initialized", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *mcpClient) rpc(method string, params, out any) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.conn.Call(ctx, method, params, out); err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
}

func (c *mcpClient) tools() []string {
	c.t.Helper()
	var out struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	c.rpc("tools/list", map[string]any{}, &out)
	var names []string
	for _, tl := range out.Tools {
		if tl.InputSchema["type"] != "object" {
			c.t.Errorf("%s inputSchema type = %v, want object", tl.Name, tl.InputSchema["type"])
		}
		names = append(names, tl.Name)
	}
	return names
}

// call runs a tool and returns its text and isError.
func (c *mcpClient) call(name string, args map[string]any) (string, bool) {
	c.t.Helper()
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	c.rpc("tools/call", map[string]any{"name": name, "arguments": args}, &out)
	if len(out.Content) != 1 || out.Content[0].Type != "text" {
		c.t.Fatalf("%s content = %+v, want one text block", name, out.Content)
	}
	return out.Content[0].Text, out.IsError
}

func (c *mcpClient) mustCall(name string, args map[string]any) string {
	c.t.Helper()
	text, isErr := c.call(name, args)
	if isErr {
		c.t.Fatalf("%s failed: %s", name, text)
	}
	return text
}

func TestMCPListsEveryWriteToolUnscoped(t *testing.T) {
	setHome(t, t.TempDir())
	got := strings.Join(newMCPClient(t, "").tools(), " ")
	want := "lathe_store lathe_verify_result lathe_extend_start lathe_extend_commit lathe_tag lathe_check_result lathe_voice_add"
	if got != want {
		t.Errorf("tools = %s, want %s", got, want)
	}
}

func TestMCPScopedServerOffersOnlyTutorialTools(t *testing.T) {
	setHome(t, t.TempDir())
	got := strings.Join(newMCPClient(t, "synth").tools(), " ")
	want := "lathe_verify_result lathe_extend_start lathe_extend_commit lathe_tag lathe_check_result"
	if got != want {
		t.Errorf("scoped tools = %s, want %s", got, want)
	}
}

func TestMCPNewSessionStoresOnlyIntoItsReservation(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutorials := filepath.Join(home, ".lathe", "tutorials")
	res, _, err := store.ReserveNew(tutorials, store.NewReservation{Topic: "build a digital synth in Zig"})
	if err != nil {
		t.Fatal(err)
	}
	writeTutorial(t, home, "other", store.StatusVerified, []string{"part-01.md"})
	c := newMCPServerClient(t, &mcp.Server{Slug: res.Slug, New: true, Version: "test"})

	got := strings.Join(c.tools(), " ")
	if want := "lathe_store lathe_verify_result lathe_extend_start lathe_extend_commit lathe_tag lathe_check_result lathe_progress"; got != want {
		t.Errorf("--new tools = %s, want %s", got, want)
	}

	// The source dir is named for some other slug: the store still lands in
	// the reservation, and nowhere else.
	src := filepath.Join(t.TempDir(), "lathe-other")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "part-01.md"), []byte("# Synth"), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, isErr := c.call("lathe_store", map[string]any{"path": src, "slug": "other"}); !isErr || !strings.Contains(text, "scoped to") {
		t.Errorf("store into another slug = %q (isError %v), want refused", text, isErr)
	}
	text := c.mustCall("lathe_store", map[string]any{"path": src, "title": "Digital Synth", "voice": "companion"})
	if !strings.Contains(text, res.Slug) {
		t.Errorf("store result = %q, want the reserved slug", text)
	}
	tut, err := store.ReadMetadata(filepath.Join(tutorials, res.Slug))
	if err != nil {
		t.Fatal(err)
	}
	if tut.Status != store.StatusUnverified || tut.Title != "Digital Synth" || len(tut.Parts) != 1 {
		t.Errorf("stored = %+v", tut)
	}
	other, _ := store.ReadMetadata(filepath.Join(tutorials, "other"))
	if other == nil || other.Status != store.StatusVerified || other.Title != "Other" {
		t.Errorf("the other tutorial changed: %+v", other)
	}

	// A reservation that was cancelled can't be resurrected by a late store.
	if err := os.RemoveAll(filepath.Join(tutorials, res.Slug)); err != nil {
		t.Fatal(err)
	}
	if text, isErr := c.call("lathe_store", map[string]any{"path": src}); !isErr || !strings.Contains(text, "gone") {
		t.Errorf("store after the reservation went = %q (isError %v), want refused", text, isErr)
	}
	if _, err := os.Stat(filepath.Join(tutorials, res.Slug)); !os.IsNotExist(err) {
		t.Errorf("late store recreated the reservation (err %v)", err)
	}
}

func TestMCPProgressReportsIntoTheReservation(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutorials := filepath.Join(home, ".lathe", "tutorials")
	res, dir, err := store.ReserveNew(tutorials, store.NewReservation{Topic: "a Game Boy emulator in Rust"})
	if err != nil {
		t.Fatal(err)
	}
	writeTutorial(t, home, "other", store.StatusVerified, []string{"part-01.md"})
	c := newMCPServerClient(t, &mcp.Server{Slug: res.Slug, New: true, Version: "test"})

	c.mustCall("lathe_progress", map[string]any{"step": "versions"})
	text := c.mustCall("lathe_progress", map[string]any{"ask": "confirm versions", "replies": []string{"Sounds right", "Other versions…"}})
	if !strings.Contains(text, "Needs you: confirm versions") {
		t.Errorf("ask result = %q", text)
	}
	w, _ := store.ReadWriting(dir)
	if w == nil || w.Step != "versions" || w.Ask == nil || len(w.Ask.Replies) != 2 {
		t.Fatalf("after the ask: %+v", w)
	}
	c.mustCall("lathe_progress", map[string]any{"step": "research", "sources": []map[string]string{{"url": "https://gbdev.io/pandocs/", "title": "Pan Docs"}}})
	w, _ = store.ReadWriting(dir)
	if w.Step != "research" || w.Ask != nil || len(w.Sources) != 1 || w.Sources[0].Title != "Pan Docs" {
		t.Errorf("after research: %+v", w)
	}
	for _, args := range []map[string]any{
		{"slug": "other", "step": "write"},
		{"step": "proofread"},
		{"replies": []string{"yes"}},
	} {
		if text, isErr := c.call("lathe_progress", args); !isErr {
			t.Errorf("lathe_progress %v = %q, want refused", args, text)
		}
	}

	// Only a --new session offers it.
	for _, srv := range []*mcp.Server{{}, {Slug: res.Slug}} {
		if got := strings.Join(newMCPServerClient(t, srv).tools(), " "); strings.Contains(got, "lathe_progress") {
			t.Errorf("server %+v offers lathe_progress: %s", srv, got)
		}
	}
}

func TestMCPUnscopedStoreRefusesSlug(t *testing.T) {
	setHome(t, t.TempDir())
	src := filepath.Join(t.TempDir(), "synth")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	c := newMCPClient(t, "")
	if text, isErr := c.call("lathe_store", map[string]any{"path": src, "slug": "x"}); !isErr || !strings.Contains(text, "slug") {
		t.Errorf("unscoped store with slug = %q (isError %v), want refused", text, isErr)
	}
}

func TestMCPCommandNewNeedsSlug(t *testing.T) {
	mcpNew = true
	t.Cleanup(func() { mcpNew = false })
	if err := mcpCmd.RunE(mcpCmd, nil); err == nil || !strings.Contains(err.Error(), "--slug") {
		t.Errorf("lathe mcp --new without --slug = %v, want an error", err)
	}
}

func TestMCPStore(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	src := filepath.Join(t.TempDir(), "digital-synth")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "part-01.md"), []byte("# One"), 0644); err != nil {
		t.Fatal(err)
	}
	c := newMCPClient(t, "")
	text := c.mustCall("lathe_store", map[string]any{
		"path":    src,
		"title":   "Digital Synth",
		"tags":    []string{"Audio", "zig,dsp"},
		"sources": []map[string]string{{"title": "Zig docs", "url": "https://ziglang.org/documentation/"}},
		"tools":   []map[string]string{{"name": "zig", "version": "0.13"}},
		"model":   "Claude Opus 4.8",
	})
	if !strings.Contains(text, "digital-synth") {
		t.Errorf("store result = %q", text)
	}
	got, err := store.ReadMetadata(filepath.Join(home, ".lathe", "tutorials", "digital-synth"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Digital Synth" || got.Model != "Claude Opus 4.8" {
		t.Errorf("metadata = %+v", got)
	}
	if strings.Join(got.Tags, ",") != "audio,dsp,zig" && strings.Join(got.Tags, ",") != "audio,zig,dsp" {
		t.Errorf("Tags = %v, want audio, zig, dsp", got.Tags)
	}
	if len(got.Sources) != 1 || got.Sources[0].Title != "Zig docs" {
		t.Errorf("Sources = %+v", got.Sources)
	}
	if len(got.Tools) != 1 || got.Tools[0] != (store.Tool{Name: "zig", Version: "0.13"}) {
		t.Errorf("Tools = %+v", got.Tools)
	}

	// A misspelled field is an error the agent sees, not a dropped value.
	if text, isErr := c.call("lathe_store", map[string]any{"path": src, "titel": "x"}); !isErr || !strings.Contains(text, "titel") {
		t.Errorf("unknown field = %q (isError %v), want an error naming it", text, isErr)
	}
}

func TestMCPVerifyResult(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutDir := writeTutorial(t, home, "synth", store.StatusUnverified, []string{"part-01.md", "part-02.md"})
	c := newMCPClient(t, "synth")

	// The slug defaults to the session's scope.
	c.mustCall("lathe_verify_result", map[string]any{"status": "verifying"})
	if tut, _ := store.ReadMetadata(tutDir); tut.Status != store.StatusVerifying {
		t.Errorf("Status = %q, want verifying", tut.Status)
	}
	c.mustCall("lathe_verify_result", map[string]any{
		"slug": "synth", "status": "failed", "part": "part-02.md", "failed_step": 3, "error": "boom",
		"checked_at": "2026-01-02T03:04:05Z",
	})
	tut, _ := store.ReadMetadata(tutDir)
	res, err := store.ReadVerifyResult(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	if tut.Status != store.StatusFailed || res.Part != "part-02.md" || res.FailedStep != 3 || res.Error != "boom" || res.CheckedAt != "2026-01-02T03:04:05Z" {
		t.Errorf("status %q, result %+v", tut.Status, res)
	}

	for name, args := range map[string]map[string]any{
		"bad status":       {"status": "great"},
		"code on skipped":  {"status": "skipped", "code": t.TempDir()},
		"another tutorial": {"slug": "other", "status": "verified"},
	} {
		if text, isErr := c.call("lathe_verify_result", args); !isErr {
			t.Errorf("%s: want an error, got %q", name, text)
		}
	}
}

func TestMCPVerifyResultRecordsStatusEvenWhenSnapshotFails(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutDir := writeTutorial(t, home, "synth", store.StatusVerifying, []string{"part-01.md"})
	c := newMCPClient(t, "synth")
	text, isErr := c.call("lathe_verify_result", map[string]any{"status": "verified", "code": t.TempDir()})
	if !isErr || !strings.Contains(text, "could not save the code snapshot") {
		t.Errorf("result = %q (isError %v), want the snapshot error", text, isErr)
	}
	if tut, _ := store.ReadMetadata(tutDir); tut.Status != store.StatusVerified {
		t.Errorf("Status = %q, want verified even though the snapshot failed", tut.Status)
	}
}

func TestMCPExtendStartAndCommit(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutDir := writeTutorial(t, home, "synth", store.StatusVerified, []string{"part-01.md"})
	c := newMCPClient(t, "synth")

	text := c.mustCall("lathe_extend_start", map[string]any{})
	if first, _, _ := strings.Cut(text, "\n"); first != "part-02.md" || !strings.Contains(text, filepath.Join(tutDir, "part-02.md")) {
		t.Errorf("extend_start = %q, want the filename then its path", text)
	}
	tut, _ := store.ReadMetadata(tutDir)
	if tut.Status != store.StatusExtending || tut.PendingPart != "part-02.md" {
		t.Errorf("after start: %+v", tut)
	}
	if text, isErr := c.call("lathe_extend_start", map[string]any{}); !isErr || !strings.Contains(text, "already extending") {
		t.Errorf("second start = %q, want already extending", text)
	}

	// Committing before the part exists fails; after writing it, succeeds.
	if _, isErr := c.call("lathe_extend_commit", map[string]any{"part": "part-02.md"}); !isErr {
		t.Error("commit of a missing part should fail")
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-02.md"), []byte("# Two"), 0644); err != nil {
		t.Fatal(err)
	}
	c.mustCall("lathe_extend_commit", map[string]any{
		"part":    "part-02.md",
		"sources": []map[string]string{{"url": "https://example.com/filters"}},
		"model":   "Claude Sonnet 4.6",
	})
	tut, _ = store.ReadMetadata(tutDir)
	if strings.Join(tut.Parts, ",") != "part-01.md,part-02.md" || tut.PendingPart != "" ||
		tut.Status != store.StatusUnverified || tut.Model != "Claude Sonnet 4.6" || len(tut.Sources) != 1 {
		t.Errorf("after commit: %+v", tut)
	}
	if _, isErr := c.call("lathe_extend_commit", map[string]any{"part": "../escape.md"}); !isErr {
		t.Error("a part path outside the tutorial should be rejected")
	}
}

func TestMCPTag(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutDir := writeTutorial(t, home, "synth", store.StatusUnverified, []string{"part-01.md"})
	c := newMCPClient(t, "")

	if text := c.mustCall("lathe_tag", map[string]any{"slug": "synth", "set": []string{"Zig", "audio,dsp"}}); !strings.Contains(text, "zig") {
		t.Errorf("tag = %q", text)
	}
	c.mustCall("lathe_tag", map[string]any{"slug": "synth", "add": []string{"synth"}, "remove": []string{"dsp"}})
	if got := tagsOf(t, tutDir); !sameTags(got, []string{"zig", "audio", "synth"}) {
		t.Errorf("tags = %v", got)
	}
	if _, isErr := c.call("lathe_tag", map[string]any{"slug": "synth"}); !isErr {
		t.Error("a tag call with nothing to do should fail")
	}
	if _, isErr := c.call("lathe_tag", map[string]any{"slug": "../etc", "add": []string{"x"}}); !isErr {
		t.Error("a path-like slug should be rejected")
	}
	if _, isErr := c.call("lathe_tag", map[string]any{"add": []string{"x"}}); !isErr {
		t.Error("an unscoped server needs a slug")
	}
}

func TestMCPCheckResult(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutDir := writeTutorial(t, home, "synth", store.StatusUnverified, []string{"part-01.md", "part-02.md"})
	c := newMCPClient(t, "synth")

	text := c.mustCall("lathe_check_result", map[string]any{
		"part":        "part-02.md",
		"status":      "fail",
		"summary":     "The checkpoint prints nothing.",
		"findings":    []map[string]string{{"file": "main.zig", "nudge": "What does the loop return?", "where": "main.zig, near the end", "reveal": "return out;"}},
		"divergences": []map[string]string{{"note": "You named it `buf`; fine.", "later_part": "part-02.md"}},
	})
	if !strings.Contains(text, "part-02.md") {
		t.Errorf("result = %q", text)
	}
	got := store.LatestCheck(tutDir, "part-02.md")
	if got == nil || got.Status != store.CheckFail || got.Findings[0].Where == "" || len(got.Divergences) != 1 {
		t.Fatalf("recorded = %+v", got)
	}
	if _, isErr := c.call("lathe_check_result", map[string]any{"part": "part-01.md", "status": "fail"}); !isErr {
		t.Error("a failing check with no findings should be rejected")
	}
	if _, isErr := c.call("lathe_check_result", map[string]any{"part": "part-01.md", "status": "pass", "hint": "x"}); !isErr {
		t.Error("an unknown field should be rejected")
	}
}

func TestMCPVoiceAdd(t *testing.T) {
	setHome(t, t.TempDir())
	c := newMCPClient(t, "")
	spec := "---\nname: terse\ndescription: Short sentences.\n---\nWrite tersely.\n"
	c.mustCall("lathe_voice_add", map[string]any{"name": "terse", "spec": spec})
	v, err := voice.Resolve("terse")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(v.Body(), "Write tersely.") {
		t.Errorf("voice body = %q", v.Body())
	}
	if _, isErr := c.call("lathe_voice_add", map[string]any{"name": "plainspoken", "spec": spec}); !isErr {
		t.Error("adding a built-in voice name should fail")
	}
}

func TestMCPUnknownToolIsAProtocolError(t *testing.T) {
	setHome(t, t.TempDir())
	c := newMCPClient(t, "synth")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// lathe_store exists, but not in a scoped session.
	err := c.conn.Call(ctx, "tools/call", map[string]any{"name": "lathe_store", "arguments": map[string]any{}}, nil)
	var rpcErr *acp.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
		t.Errorf("err = %v, want invalid params", err)
	}
}

// TestMCPCommandServesStdio runs the real `lathe mcp` command over stdin and
// stdout, the way an agent launches it from session/new.
func TestMCPCommandServesStdio(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutDir := writeTutorial(t, home, "synth", store.StatusUnverified, []string{"part-01.md"})
	mcpSlug = "synth"
	t.Cleanup(func() { mcpSlug = "" })

	frames := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "t", "version": "0"}}},
		{"jsonrpc": "2.0", "method": "notifications/initialized"},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "lathe_tag", "arguments": map[string]any{"set": []string{"zig"}}}},
	}
	var in bytes.Buffer
	for _, f := range frames {
		raw, _ := json.Marshal(f)
		in.Write(append(raw, '\n'))
	}
	// stdin ends right after the last request; the server still answers it.
	var out bytes.Buffer
	mcpCmd.SetIn(&in)
	mcpCmd.SetOut(&out)
	t.Cleanup(func() { mcpCmd.SetIn(nil); mcpCmd.SetOut(nil) })
	if err := mcpCmd.RunE(mcpCmd, nil); err != nil {
		t.Fatalf("lathe mcp: %v", err)
	}
	if !strings.Contains(out.String(), `"protocolVersion":"2025-03-26"`) {
		t.Errorf("initialize should echo a known protocol version:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `Tags for \"synth\": zig`) {
		t.Errorf("tools/call output:\n%s", out.String())
	}
	if got := tagsOf(t, tutDir); !sameTags(got, []string{"zig"}) {
		t.Errorf("tags = %v", got)
	}
}
