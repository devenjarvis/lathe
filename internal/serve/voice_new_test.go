package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/devenjarvis/lathe/internal/acp/acptest"
	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/mcp"
	"github.com/devenjarvis/lathe/internal/voice"
)

func voiceSpec(sample string) string {
	return "---\nname: field-notes\ndescription: Lab notebook lines.\n---\n\n# Field notes\n\n## Stance and register\n\n- Short labelled lines.\n\n## Sample\n\n" + sample + "\n"
}

func voiceState(t *testing.T, ts *httptest.Server, id string) voiceJobView {
	t.Helper()
	resp, err := http.Get(ts.URL + "/-/voices/new/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET state = %d", resp.StatusCode)
	}
	var v voiceJobView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func waitVoiceState(t *testing.T, ts *httptest.Server, id, state string) voiceJobView {
	t.Helper()
	var v voiceJobView
	waitFor(t, "voice state "+state, func() bool {
		v = voiceState(t, ts, id)
		return v.State == state
	})
	return v
}

func TestNewVoiceDraftRefineSave(t *testing.T) {
	f := &fakeHost{}
	var (
		mu      sync.Mutex
		results []string
	)
	_, ts, _ := newTutServer(t, f, func() *acptest.Agent {
		return &acptest.Agent{Reply: func(prompt string) string {
			m := &mcp.Server{VoiceDraft: "field-notes"}
			var text string
			var isErr bool
			switch {
			case strings.Contains(prompt, "clicked Save voice on draft"):
				text, isErr = mcpCall(t, m, "lathe_voice_add", map[string]any{})
			case strings.Contains(prompt, "Their change: Shorter"):
				text, isErr = mcpCall(t, m, "lathe_voice_draft", map[string]any{"spec": voiceSpec("Wraps. Check it.")})
			default:
				// Saving before the reader's Save voice is refused.
				early, earlyErr := mcpCall(t, m, "lathe_voice_add", map[string]any{})
				mu.Lock()
				results = append(results, early)
				mu.Unlock()
				if !earlyErr {
					t.Error("lathe_voice_add before Save voice should fail")
				}
				text, isErr = mcpCall(t, m, "lathe_voice_draft", map[string]any{"spec": voiceSpec("Note: the buffer wraps.")})
			}
			mu.Lock()
			results = append(results, text)
			mu.Unlock()
			if isErr {
				t.Errorf("tool failed: %s", text)
			}
			return "Here is a draft."
		}}
	})

	code, out := postJob(t, ts, "/-/voices/new", `{"name":"Field-Notes","describe":"Like a lab notebook.","base":"plainspoken","writing":"Short. Clear."}`)
	if code != http.StatusOK || out["mode"] != "agent" || out["name"] != "field-notes" || !strings.HasSuffix(out["path"], "field-notes.md") {
		t.Fatalf("POST /-/voices/new = %d %v", code, out)
	}
	id := out["runId"]
	v := waitVoiceState(t, ts, id, "waiting")
	if len(v.Drafts) != 1 || v.Drafts[0].N != 1 || strings.Join(v.Drafts[0].Sample, " ") != "Note: the buffer wraps." || v.Agent == "" {
		t.Fatalf("after draft 1: %+v", v)
	}
	if !strings.Contains(v.Reply, "Here is a draft.") {
		t.Errorf("reply = %q", v.Reply)
	}
	// The name is reserved, not a voice.
	if voiceSaved("field-notes") {
		t.Error("a draft should not be a voice yet")
	}

	a := f.agents[0]
	first := a.Prompts[0]
	for _, want := range []string{"reserved the name field-notes", "How it should sound: Like a lab notebook.", `<start-from-voice name="plainspoken">`, "<writing-the-reader-likes>\nShort. Clear.", `<skill name="lathe-voice">`, "Hosted: Settings › Voices › New voice", "\n/lathe-voice field-notes"} {
		if !strings.Contains(first, want) {
			t.Errorf("first prompt lacks %q", want)
		}
	}
	if strings.Contains(first, voice.Preamble) {
		t.Error("the prompt should not carry the guardrail preamble as editable text")
	}
	if mcpArgs := string(a.MCPServers[0]); !strings.Contains(mcpArgs, `"--voice-draft","field-notes"`) || strings.Contains(mcpArgs, "--slug") {
		t.Errorf("session MCP = %s, want the voice-draft scope", mcpArgs)
	}

	// Save needs a waiting run; refine first.
	code, _ = postJob(t, ts, "/-/voices/new/"+id+"/refine", `{"text":"Shorter"}`)
	if code != http.StatusNoContent {
		t.Fatalf("refine = %d", code)
	}
	waitFor(t, "draft 2", func() bool { v = voiceState(t, ts, id); return v.State == "waiting" && len(v.Drafts) == 2 })
	if v.Drafts[1].N != 2 || v.Drafts[1].Sample[0] != "Wraps. Check it." {
		t.Errorf("draft 2 = %+v", v.Drafts[1])
	}

	// Save draft 1 (not the latest) as the default: exactly draft 1 lands.
	code, _ = postJob(t, ts, "/-/voices/new/"+id+"/save", `{"draft":1,"makeDefault":true}`)
	if code != http.StatusNoContent {
		t.Fatalf("save = %d", code)
	}
	waitVoiceState(t, ts, id, "saved")
	saved, err := voice.Resolve("field-notes")
	if err != nil || saved.Sample() != "Note: the buffer wraps." {
		t.Fatalf("saved = %q, %v", saved.Sample(), err)
	}
	if got := config.DefaultVoice(); got != "field-notes" {
		t.Errorf("default = %q, want field-notes", got)
	}
	if dir, _ := voice.DraftDir("field-notes"); dirExists(dir) {
		t.Error("the reservation should be gone once the run ends")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(results) == 0 || !strings.Contains(results[0], "Save voice") {
		t.Errorf("early save result = %v", results)
	}
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func TestNewVoiceCancelDropsTheReservation(t *testing.T) {
	f := &fakeHost{}
	_, ts, _ := newTutServer(t, f, func() *acptest.Agent {
		return &acptest.Agent{Reply: func(prompt string) string {
			_, _ = mcpCall(t, &mcp.Server{VoiceDraft: "terse"}, "lathe_voice_draft", map[string]any{"spec": voiceSpec("Short.")})
			return "Draft."
		}}
	})
	code, out := postJob(t, ts, "/-/voices/new", `{"name":"terse","describe":"Terse.","base":""}`)
	if code != http.StatusOK {
		t.Fatalf("POST = %d %v", code, out)
	}
	id := out["runId"]
	waitVoiceState(t, ts, id, "waiting")
	if !strings.Contains(f.agents[0].Prompts[0], "Start from: blank") {
		t.Error("a blank start should say so")
	}
	// A second series for the same name is refused while this one runs.
	if code, _ := postJob(t, ts, "/-/voices/new", `{"name":"terse","describe":"Terse."}`); code != http.StatusConflict {
		t.Errorf("second series = %d, want 409", code)
	}
	if code, _ := postJob(t, ts, "/-/voices/new/"+id+"/cancel", `{}`); code != http.StatusNoContent {
		t.Fatalf("cancel = %d", code)
	}
	v := voiceState(t, ts, id)
	if v.State != "stopped" || v.Command != "/lathe-voice terse" {
		t.Errorf("after cancel: %+v", v)
	}
	if voiceSaved("terse") {
		t.Error("cancel saved the voice")
	}
	if dir, _ := voice.DraftDir("terse"); dirExists(dir) {
		t.Error("cancel left the reservation")
	}
	// Refine and save on a finished run are refused.
	if code, _ := postJob(t, ts, "/-/voices/new/"+id+"/save", `{"draft":1}`); code != http.StatusConflict {
		t.Errorf("save after cancel = %d", code)
	}
}

func TestNewVoiceValidatesAndHandsOffWithoutAnAgent(t *testing.T) {
	_, ts, _ := newTutServer(t, &fakeHost{}, nil)
	if err := voice.Add("mine", []byte("---\nname: mine\n---\n\n# Mine\n")); err != nil {
		t.Fatal(err)
	}
	for body, want := range map[string]int{
		`{"name":"","describe":"x"}`:                 http.StatusBadRequest,
		`{"name":"Not a slug","describe":"x"}`:       http.StatusBadRequest,
		`{"name":"ok","describe":""}`:                http.StatusBadRequest,
		`{"name":"plainspoken","describe":"x"}`:      http.StatusConflict,
		`{"name":"mine","describe":"x"}`:             http.StatusConflict,
		`{"name":"ok","describe":"x","base":"nope"}`: http.StatusBadRequest,
	} {
		if code, _ := postJob(t, ts, "/-/voices/new", body); code != want {
			t.Errorf("%s = %d, want %d", body, code, want)
		}
	}
	code, out := postJob(t, ts, "/-/voices/new", `{"name":"terse","describe":"Terse."}`)
	if code != http.StatusOK || out["mode"] != "handoff" || out["command"] != "/lathe-voice terse" {
		t.Errorf("no agent = %d %v, want the /lathe-voice handoff", code, out)
	}
	if dir, _ := voice.DraftDir("terse"); dirExists(dir) {
		t.Error("the handoff should reserve nothing")
	}
	page := get(t, ts, "/-/settings?voice=new")
	for _, want := range []string{"How should it sound?", "Paste writing you like", "Always on.", "No agent is set up", "/lathe-voice &lt;name&gt;"} {
		if !strings.Contains(page, want) {
			t.Errorf("New voice pane without an agent lacks %q", want)
		}
	}
	if strings.Contains(page, `id="vnSave"`) {
		t.Error("no Save voice without an agent")
	}
}

func TestNewVoicePaneWithAnAgent(t *testing.T) {
	_, ts, _ := newTutServer(t, &fakeHost{}, func() *acptest.Agent { return &acptest.Agent{} })
	page := get(t, ts, "/-/settings?voice=new")
	for _, want := range []string{`data-voice="new" aria-labelledby="voice-title-new">`, "Draft it", "Drafts with", `data-refine="Shorter"`, `data-refine="Warmer"`, `data-refine="Less jargon"`, `data-refine="More examples"`, "Make it my default", "Save voice", `value="companion"`, "<span>Blank</span>", "can't be edited"} {
		if !strings.Contains(page, want) {
			t.Errorf("New voice pane lacks %q", want)
		}
	}
	if strings.Contains(page, "No agent is set up") {
		t.Error("the handoff note shows with an agent")
	}
}
