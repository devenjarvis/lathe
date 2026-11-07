package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/devenjarvis/lathe/internal/voice"
)

// The tools of a New voice session (`lathe mcp --voice-draft <name>`), the
// voice counterpart of a New tutorial's reserve + scoped store: the server
// reserved <name> (voice.ReserveDraft) before the agent started.
//
//   - lathe_voice_draft records the next draft in the reservation, where the
//     Settings page reads it for the preview. It writes no voice.
//   - lathe_voice_show reads a voice's spec (a built-in to start from).
//   - lathe_voice_add saves under <name> only, and only the draft the reader
//     approved with Save voice (voice.SaveApproved): exactly as drafted, so
//     what is saved is what the reader saw. Before Save voice it refuses.
func voiceDraftTools(name string) []Tool {
	return []Tool{
		{
			Name:        "lathe_voice_draft",
			Title:       "Record a voice draft",
			Description: fmt.Sprintf("Record the next draft of the new voice %q for the reader's preview in Settings › Voices. Pass the whole spec: name:/description: frontmatter, # Title, ## Stance and register, ## Avoid, ## Calibration — before / after, and last ## Sample (the ring buffer paragraph in this voice, which the reader sees). It does not save the voice. Returns the draft number.", name),
			InputSchema: obj([]string{"spec"}, map[string]any{
				"spec": str("the full voice spec markdown, without Lathe's guardrail preamble"),
			}),
			run: runVoiceDraft,
		},
		{
			Name:        "lathe_voice_show",
			Title:       "Read a voice's spec",
			Description: "Read a voice's spec (same as `lathe voice show <name>`, without the guardrail preamble): its description and the sections that steer generation. Use it to start from a built-in (plainspoken, companion).",
			InputSchema: obj([]string{"name"}, map[string]any{
				"name": str("the voice to read, e.g. plainspoken"),
			}),
			run: runVoiceShow,
		},
		{
			Name:        "lathe_voice_add",
			Title:       "Save the approved voice",
			Description: fmt.Sprintf("Save the new voice %q once the reader has clicked Save voice. It saves the draft they approved exactly as you drafted it, so it takes no spec. Before they click Save voice it refuses.", name),
			InputSchema: obj(nil, map[string]any{
				"name": str(fmt.Sprintf("the voice's name; this session may save only %q, which is the default", name)),
			}),
			run: runVoiceAddApproved,
		},
	}
}

func runVoiceDraft(s *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Spec string `json:"spec"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	n, err := voice.AddDraft(s.VoiceDraft, a.Spec)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Recorded draft %d of %q; the reader sees its sample now. End your turn with one short sentence about this draft and wait for the reader.", n, s.VoiceDraft), nil
}

func runVoiceShow(_ *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	v, err := voice.Resolve(a.Name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("description: %s\n\n%s", v.Description, strings.TrimSpace(v.Body())), nil
}

func runVoiceAddApproved(s *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := decode(raw, &a); err != nil {
		return "", fmt.Errorf("%w (this session's lathe_voice_add saves the draft the reader approved, so it takes no spec)", err)
	}
	if n := strings.ToLower(strings.TrimSpace(a.Name)); n != "" && n != s.VoiceDraft {
		return "", fmt.Errorf("this Lathe session is scoped to the new voice %q; it cannot save %q", s.VoiceDraft, n)
	}
	v, n, err := voice.SaveApproved(s.VoiceDraft)
	if errors.Is(err, voice.ErrNotApproved) {
		return "", errors.New("the reader hasn't clicked Save voice yet. Only the reader saves: record drafts with lathe_voice_draft and end your turn")
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Saved draft %d as the custom voice %q.", n, v.Name), nil
}
