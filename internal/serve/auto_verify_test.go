package serve

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/acp/acptest"
	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

// autoVerifyAgent is a fake agent that plays both skills: an extend prompt
// reserves, writes and commits a part (unless commit is false), and a verify
// prompt records "verified". It records which jobs it was prompted for.
type autoVerifyAgent struct {
	mu      sync.Mutex
	jobs    []string
	scratch string // the verify prompt's scratch dir
	commit  bool
}

func (a *autoVerifyAgent) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.jobs...)
}

func (a *autoVerifyAgent) make(t *testing.T) func(tutDir string) *acptest.Agent {
	return func(tutDir string) *acptest.Agent {
		return &acptest.Agent{Reply: func(prompt string) string {
			if strings.Contains(prompt, `<skill name="lathe-verify">`) {
				a.mu.Lock()
				a.jobs = append(a.jobs, jobVerify)
				a.scratch = scratchOf(t, prompt)
				a.mu.Unlock()
				if err := store.RecordVerify(tutDir, store.VerifyRecord{Status: store.StatusVerifying}); err != nil {
					t.Error(err)
				}
				if err := store.RecordVerify(tutDir, store.VerifyRecord{Status: store.StatusVerified}); err != nil {
					t.Error(err)
				}
				return "Verified."
			}
			a.mu.Lock()
			a.jobs = append(a.jobs, jobExtend)
			a.mu.Unlock()
			if !a.commit {
				return "I looked around and wrote nothing."
			}
			name, err := store.ExtendStart(tutDir)
			if err != nil {
				t.Error(err)
				return "no"
			}
			if err := os.WriteFile(filepath.Join(tutDir, name), []byte("# Part 3\n"), 0o644); err != nil {
				t.Error(err)
			}
			if err := store.ExtendCommit(tutDir, name, nil, "Test Model"); err != nil {
				t.Error(err)
			}
			return "Part 3 adds filters."
		}}
	}
}

func setAutoVerify(t *testing.T, on bool) {
	t.Helper()
	if err := config.UpdateConfig(func(c *config.Config) error { c.AutoVerify = on; return nil }); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAutoVerifyAfterHostedExtend(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setAutoVerify(t, true)
	a := &autoVerifyAgent{commit: true}
	srv, ts, tutDir := newJobServer(t, &fakeHost{}, a.make(t))

	code, out := postJob(t, ts, "/-/extend/series", "")
	if code != http.StatusOK || out["mode"] != "agent" {
		t.Fatalf("extend = %d %v", code, out)
	}
	if end := last(eventStream(t, ts, out["runId"], nil)); end.Type != "done" || end.Message != "" {
		t.Fatalf("extend ended with %+v", end)
	}

	// The follow-up verify starts on its own, through startJob: the status
	// endpoint names it so the reading page attaches, and it records a result.
	waitFor(t, "the auto verify to finish", func() bool {
		tut, err := store.ReadMetadata(tutDir)
		return err == nil && tut.Status == store.StatusVerified && srv.runningJob("series") == nil
	})
	if got := a.seen(); len(got) != 2 || got[0] != jobExtend || got[1] != jobVerify {
		t.Errorf("agent jobs = %v, want [extend verify]", got)
	}
	tut, _ := store.ReadMetadata(tutDir)
	if len(tut.Parts) != 3 {
		t.Errorf("parts = %v", tut.Parts)
	}
	// The verify ran with a Lathe-made scratch dir, removed afterwards.
	a.mu.Lock()
	scratch := a.scratch
	a.mu.Unlock()
	if !strings.Contains(scratch, "lathe-verify-") {
		t.Errorf("verify scratch dir = %q", scratch)
	}
	waitFor(t, "the scratch dir to go", func() bool { _, err := os.Stat(scratch); return os.IsNotExist(err) })
}

func TestAutoVerifyOffByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &autoVerifyAgent{commit: true}
	srv, ts, tutDir := newJobServer(t, &fakeHost{}, a.make(t))
	code, out := postJob(t, ts, "/-/extend/series", "")
	if code != http.StatusOK {
		t.Fatalf("extend = %d %v", code, out)
	}
	last(eventStream(t, ts, out["runId"], nil))
	time.Sleep(200 * time.Millisecond)
	if got := a.seen(); len(got) != 1 {
		t.Errorf("agent jobs = %v, want only the extend", got)
	}
	if srv.runningJob("series") != nil {
		t.Error("a job is running after an extend with auto_verify off")
	}
	if tut, _ := store.ReadMetadata(tutDir); tut.Status != store.StatusUnverified {
		t.Errorf("status = %q, want unverified", tut.Status)
	}
}

func TestAutoVerifySkipsAnExtendWithoutANewPart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setAutoVerify(t, true)
	a := &autoVerifyAgent{commit: false}
	_, ts, _ := newJobServer(t, &fakeHost{}, a.make(t))
	_, out := postJob(t, ts, "/-/extend/series", "")
	last(eventStream(t, ts, out["runId"], nil))
	time.Sleep(200 * time.Millisecond)
	if got := a.seen(); len(got) != 1 {
		t.Errorf("agent jobs = %v, want only the extend (no part was committed)", got)
	}
}

func TestAutoVerifyNotForWorkerOrHandoff(t *testing.T) {
	// No hosted agent: an extend hands off, and nothing else is queued or
	// started even with auto_verify on.
	_, ts, _ := settingsServer(t)
	setAutoVerify(t, true)
	code, out := postJob(t, ts, "/-/extend/series", "")
	if code != http.StatusOK || out["mode"] != "handoff" {
		t.Fatalf("extend = %d %v, want the handoff", code, out)
	}
}
