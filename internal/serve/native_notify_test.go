package serve

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

// actionNative is a shell with actionable notifications and a badge.
type actionNative struct {
	*fakeNative
	notices chan Notice
	badges  chan int
}

func newActionNative() *actionNative {
	return &actionNative{fakeNative: newFakeNative(), notices: make(chan Notice, 8), badges: make(chan int, 8)}
}

func (a *actionNative) NotifyWithAction(n Notice) { a.notices <- n }
func (a *actionNative) SetBadge(n int)            { a.badges <- n }

func (a *actionNative) nextBadge(t *testing.T) int {
	t.Helper()
	select {
	case n := <-a.badges:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("no badge")
		return -1
	}
}

func (a *actionNative) nextAction(t *testing.T) Notice {
	t.Helper()
	select {
	case n := <-a.notices:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("no notification with an action")
		return Notice{}
	}
}

func TestJobActions(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)

	check := func(o jobOutcome, label, path string) {
		t.Helper()
		a := srv.jobAction(o)
		if a == nil || a.Label != label || a.Path != path {
			t.Errorf("jobAction(%s) = %+v, want %s %s", o.job, a, label, path)
		}
		if _, ok := srv.tutorialPage(a.Path); !ok {
			t.Errorf("action path %q is not a reading page", a.Path)
		}
	}
	check(jobOutcome{job: jobVerify, tutDir: tutDir}, "Open", "/series/")
	check(jobOutcome{job: jobExtend, tutDir: tutDir}, "Open", "/series/part-02.md")
	check(jobOutcome{job: jobCheck, tutDir: tutDir, part: "part-01.md"}, "View feedback", "/series/part-01.md#checkWork")
	check(jobOutcome{job: jobNew, tutDir: tutDir}, "Open", "/series/")

	tut, _ := store.ReadMetadata(tutDir)
	tut.Status = store.StatusFailed
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteVerifyResult(tutDir, &store.VerifyResult{Part: "part-02.md", Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	check(jobOutcome{job: jobVerify, tutDir: tutDir}, "View report", "/series/part-02.md#verifyRegion")

	if a := srv.jobAction(jobOutcome{job: jobVerify, tutDir: filepath.Join(dir, "gone")}); a != nil {
		t.Errorf("gone tutorial action = %+v", a)
	}
}

func TestNotifyJobCarriesAnActionOrFallsBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	tutDir := makeTutFixture(t, dir, "series", true)

	// A shell with actions gets the action.
	srv := NewServer(dir)
	an := newActionNative()
	srv.SetNative(an)
	t.Cleanup(srv.Close)
	srv.notifyJob(jobOutcome{job: jobExtend, tutDir: tutDir})
	n := an.nextAction(t)
	if n.Title != "Extend finished: Test Tutorial" || n.Action == nil || n.Action.Label != "Open" || n.Action.Path != "/series/part-02.md" {
		t.Errorf("notice = %+v", n)
	}

	// A plain shell gets the plain notification, as before.
	plain := NewServer(dir)
	fn := newFakeNative()
	plain.SetNative(fn)
	t.Cleanup(plain.Close)
	plain.notifyJob(jobOutcome{job: jobExtend, tutDir: tutDir})
	if got := fn.nextNotice(t); got[0] != "Extend finished: Test Tutorial" {
		t.Errorf("plain notice = %v", got)
	}

	// An action that isn't a reading page is dropped, not opened.
	srv.postNotice(an, Notice{Title: "x", Action: &NotifyAction{Label: "Open", Path: "//evil.example/"}})
	if got := an.nextNotice(t); got[0] != "x" {
		t.Errorf("fallback notice = %v", got)
	}
}

func TestAttentionWatch(t *testing.T) {
	var w attentionWatch
	fresh, count, changed := w.step(nil)
	if len(fresh) != 0 || count != 0 || !changed {
		t.Errorf("first step = %v %d %v, want a 0 badge", fresh, count, changed)
	}
	a, b := attentionItem{key: "1/p1"}, attentionItem{key: "2/input"}
	if fresh, count, changed = w.step([]attentionItem{a}); len(fresh) != 1 || count != 1 || !changed {
		t.Errorf("step a = %v %d %v", fresh, count, changed)
	}
	if fresh, count, changed = w.step([]attentionItem{a}); len(fresh) != 0 || count != 1 || changed {
		t.Errorf("step a again = %v %d %v", fresh, count, changed)
	}
	if fresh, count, _ = w.step([]attentionItem{a, b}); len(fresh) != 1 || fresh[0].key != b.key || count != 2 {
		t.Errorf("step a,b = %v %d", fresh, count)
	}
	if fresh, count, changed = w.step(nil); len(fresh) != 0 || count != 0 || !changed {
		t.Errorf("step none = %v %d %v", fresh, count, changed)
	}
}

// waitingRun is an unfinished run with an open permission prompt.
func waitingRun(id, job string, target jobTarget) *agentRun {
	ctx, cancel := context.WithCancel(context.Background())
	r := &agentRun{id: id, job: job, ctx: ctx, cancel: cancel, notify: make(chan struct{}), perms: map[string]chan string{"p1": make(chan string, 1)}, done: make(chan struct{}), target: target, started: time.Now()}
	r.events = []runEvent{{Type: "permission", ID: "p1", Title: "Run zig build"}}
	return r
}

func TestNeedsYouNotificationsAndBadge(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	tutDir := makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	an := newActionNative()
	srv.nativeMu.Lock()
	srv.native = an // no loop: the test drives attentionTick
	srv.nativeMu.Unlock()

	h := newAgentHost("claude-code-acp", nil)
	h.name = "Claude Code"
	verify := waitingRun("1", jobVerify, jobTarget{tutDir: tutDir})
	ask := waitingRun("2", jobAsk, jobTarget{})
	ask.perms = map[string]chan string{}
	ask.events = nil
	ask.waiting = true
	h.runs["1"], h.runs["2"] = verify, ask
	h.active["series/part-02.md"] = "2"
	srv.setAgentHost(h)

	var w attentionWatch
	srv.attentionTick(&w)
	if got := an.nextBadge(t); got != 2 {
		t.Errorf("badge = %d, want 2", got)
	}
	got := map[string]Notice{}
	for range 2 {
		n := an.nextAction(t)
		got[n.Action.Path] = n
	}
	if n := got["/series/#agent"]; n.Title != "Claude Code needs you" || n.Body != "Run zig build" || n.Action.Label != "Answer" {
		t.Errorf("verify prompt notice = %+v", n)
	}
	if n := got["/series/part-02.md#agent"]; n.Body != "It's waiting for your answer." {
		t.Errorf("ask notice = %+v (all: %+v)", n, got)
	}

	// Nothing new: no notification, no badge change.
	srv.attentionTick(&w)
	select {
	case n := <-an.notices:
		t.Errorf("repeat notice %+v", n)
	case b := <-an.badges:
		t.Errorf("repeat badge %d", b)
	case <-time.After(50 * time.Millisecond):
	}

	// The reader answers one: the badge drops.
	verify.mu.Lock()
	delete(verify.perms, "p1")
	verify.mu.Unlock()
	srv.attentionTick(&w)
	if b := an.nextBadge(t); b != 1 {
		t.Errorf("badge = %d, want 1", b)
	}

	// With the toggle off a new prompt still counts, but posts nothing.
	if err := config.UpdateConfig(func(c *config.Config) error {
		c.Notify = &config.NotifyConfig{NeedsYou: config.Bool(false)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	verify.mu.Lock()
	verify.perms["p2"] = make(chan string, 1)
	verify.mu.Unlock()
	srv.attentionTick(&w)
	if b := an.nextBadge(t); b != 2 {
		t.Errorf("badge = %d, want 2", b)
	}
	select {
	case n := <-an.notices:
		t.Errorf("notified with the toggle off: %+v", n)
	case <-time.After(50 * time.Millisecond):
	}
}
