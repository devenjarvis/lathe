package serve

import (
	"log"
	"path/filepath"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

// Auto-verify (Settings › Library, config key auto_verify, off by default).
// Verification stays opt-in: the reader turns this on to say "verify every
// new part". When a hosted-agent Extend run finishes and committed a new
// part, Lathe starts a Verify run for the tutorial through the same path
// POST /-/verify uses (startJob on the hosted agent), so it streams into the
// reading page like a clicked Verify: the page's status poller sees the run
// in /-/status and attaches to it. Only the hosted agent does this; a
// /lathe-work worker or a pasted handoff is the reader's own session and
// Lathe doesn't queue more work for it.

// partCount is how many parts the tutorial lists, or 0 when its metadata
// can't be read.
func partCount(tutDir string) int {
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		return 0
	}
	return len(tut.Parts)
}

// maybeAutoVerify starts the follow-up Verify for a finished Extend run when
// auto_verify is on. runFinished calls it for runs the reader didn't cancel.
func (s *Server) maybeAutoVerify(run *agentRun, final runEvent) {
	if run.job != jobExtend || final.Type != "done" || final.Message != "" {
		// Not an extend, or one that crashed, failed, or ended without
		// committing (settleJob's note is in Message).
		return
	}
	if !config.LoadSettings().AutoVerify {
		return
	}
	tutDir := run.target.tutDir
	tut, err := store.ReadMetadata(tutDir)
	if err != nil || len(tut.Parts) <= run.target.parts {
		return // no new part registered
	}
	if tut.Status == store.StatusExtending || tut.Status == store.StatusVerifying {
		return
	}
	agent := s.hostedAgent()
	if agent == nil {
		return
	}
	slug := filepath.Base(tutDir)
	// drive calls runFinished before the extend run's done channel closes;
	// start the verify on its own goroutine so the extend finishes first.
	go func() {
		if _, err := agent.startJob(jobVerify, tutDir, slug, "/lathe-verify "+slug); err != nil {
			log.Printf("lathe: auto-verify for %s did not start: %v", slug, err)
		}
	}()
}
