package extend

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/verify"
)

//go:embed skills/lathe-extend.md
var extendSkillContent string

// NextPartFilename returns the zero-padded part-NN.md filename that follows
// the last entry in parts, or "part-01.md" when parts is empty.
func NextPartFilename(parts []string) string {
	return fmt.Sprintf("part-%02d.md", len(parts)+1)
}

// SpawnExtender writes metadata (status=extending, pending_part=next) before
// spawning the claude subprocess so the UI shows the in-flight badge even if
// spawn fails. On success the goroutine appends to Parts, clears PendingPart,
// sets status=verifying, and calls verify.SpawnVerifier.
func SpawnExtender(slug, tutorialDir, guidance string) error {
	tut, err := store.ReadMetadata(tutorialDir)
	if err != nil {
		return fmt.Errorf("read metadata: %w", err)
	}

	if tut.Status == store.StatusExtending || tut.Status == store.StatusVerifying {
		return fmt.Errorf("already extending or verifying: status is %q", tut.Status)
	}

	pendingPart := NextPartFilename(tut.Parts)

	tut.Status = store.StatusExtending
	tut.PendingPart = pendingPart
	if err := store.WriteMetadata(tutorialDir, tut); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "lathe-extend-"+slug+"-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}

	skillDir := filepath.Join(tempDir, ".claude", "skills", "lathe-extend")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		os.RemoveAll(tempDir)
		return fmt.Errorf("create skill dir: %w", err)
	}
	skillPath := filepath.Join(skillDir, "lathe-extend.md")
	if err := os.WriteFile(skillPath, []byte(extendSkillContent), 0644); err != nil {
		os.RemoveAll(tempDir)
		return fmt.Errorf("write skill: %w", err)
	}

	system := buildSystemPrompt(tut, tutorialDir)
	user := buildUserPrompt(tut, tutorialDir, pendingPart, guidance)

	cmd := exec.Command("claude",
		"--add-dir", tempDir,
		"--add-dir", tutorialDir,
		"--dangerously-skip-permissions",
		"--allowedTools", "Read,Glob,Grep,Write",
		"--system-prompt", system,
		"-p", user,
	)
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		os.RemoveAll(tempDir)
		// Leave metadata as "extending" — same contract as verify.SpawnVerifier:
		// the status was committed before spawn; callers observe it in-flight.
		return fmt.Errorf("start extender: %w", err)
	}

	go func() {
		defer os.RemoveAll(tempDir)
		waitErr := cmd.Wait()
		partPath := filepath.Join(tutorialDir, pendingPart)
		if waitErr == nil {
			if _, statErr := os.Stat(partPath); statErr == nil {
				// Success: append part, clear pending, chain to verifier
				current, readErr := store.ReadMetadata(tutorialDir)
				if readErr == nil {
					current.Parts = append(current.Parts, pendingPart)
					current.PendingPart = ""
					current.Status = store.StatusVerifying
					if writeErr := store.WriteMetadata(tutorialDir, current); writeErr == nil {
						verify.SpawnVerifier(slug, tutorialDir) //nolint:errcheck
					}
					return
				}
			}
		}
		// Failure: flip to failed
		current, readErr := store.ReadMetadata(tutorialDir)
		if readErr == nil {
			store.WriteMetadata(tutorialDir, failedMeta(current)) //nolint:errcheck
		}
	}()

	return nil
}

func failedMeta(tut *store.Tutorial) *store.Tutorial {
	tut.Status = store.StatusFailed
	tut.PendingPart = ""
	return tut
}

func buildSystemPrompt(tut *store.Tutorial, tutorialDir string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are continuing the tutorial %q (topic: %s).\n\n", tut.Title, tut.Topic)
	b.WriteString("Below are all existing parts in order. Read them to understand the arc, voice, and controlling example.\n\n")
	for _, p := range tut.Parts {
		data, err := os.ReadFile(filepath.Join(tutorialDir, p))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "--- BEGIN %s ---\n", p)
		b.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "--- END %s ---\n\n", p)
	}
	return b.String()
}

func buildUserPrompt(tut *store.Tutorial, tutorialDir, pendingPart, guidance string) string {
	var b strings.Builder
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(pendingPart, "part-"), ".md"))
	fmt.Fprintf(&b, "Write Part %d of the tutorial. Save it as %q inside the tutorial directory %q.\n",
		n, pendingPart, tutorialDir)
	if guidance != "" {
		fmt.Fprintf(&b, "\nUser guidance for this part: %s\n", guidance)
	}
	return b.String()
}
