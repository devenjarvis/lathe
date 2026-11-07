package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/voice"
)

func addVoice(t *testing.T, name, sample string) {
	t.Helper()
	spec := "---\nname: " + name + "\ndescription: A test voice.\n---\n\n# " + name + "\n\nDry.\n"
	if sample != "" {
		spec += "\n## Sample\n\n" + sample + "\n"
	}
	if err := voice.Add(name, []byte(spec)); err != nil {
		t.Fatal(err)
	}
}

func setTutorialVoice(t *testing.T, tutDir, name string) {
	t.Helper()
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatal(err)
	}
	tut.Voice = name
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
}

func TestVoicesTabListsAndCompares(t *testing.T) {
	_, ts, tutDir := settingsServer(t)
	addVoice(t, "terse", "")
	setTutorialVoice(t, tutDir, "companion")

	page := get(t, ts, "/-/settings")
	for _, want := range []string{
		`data-voice-pick="plainspoken"`, `data-voice-pick="companion"`, `data-voice-pick="terse"`,
		"Built in", "Yours", "+ New voice", "/lathe-voice &lt;name&gt;",
		"Used by 1 tutorial", "Default for new tutorials", "Make default",
		"Sounds like", "No sample yet", "Compare samples",
		"Duplicate and edit", "Edit with agent", "Delete voice…", "/lathe-voice terse",
		// A browser can't reveal a file: it copies the path instead.
		"Copy file path",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("voices tab lacks %q", want)
		}
	}
	// Both built-ins' samples are on the page (detail + compare).
	if c := strings.Count(page, "ring buffer"); c < 4 {
		t.Errorf("ring buffer samples on the page = %d, want each built-in's in its pane and in Compare", c)
	}
	// The pane opens on the default; ?voice= opens another, and an unknown
	// name falls back to the default.
	if !strings.Contains(page, `href="?voice=plainspoken#voices" data-voice-pick="plainspoken" aria-current="true"`) {
		t.Error("voices tab doesn't open on the default")
	}
	page = get(t, ts, "/-/settings?voice=terse")
	if !strings.Contains(page, `data-voice-pick="terse" aria-current="true"`) || !strings.Contains(page, `<article class="voice-detail" data-voice="terse" aria-labelledby="voice-title-terse">`) {
		t.Error("?voice=terse doesn't select terse")
	}
	page = get(t, ts, "/-/settings?voice=compare")
	if !strings.Contains(page, `data-voice="compare" aria-labelledby="voice-title-compare">`) || strings.Contains(page, `aria-current="true"><span class="voices-item-name">`) {
		t.Error("?voice=compare should open Compare with no voice selected")
	}
	if page := get(t, ts, "/-/settings?voice=nope"); !strings.Contains(page, `data-voice-pick="plainspoken" aria-current="true"`) {
		t.Error("an unknown ?voice= should open the default")
	}
}

func TestVoicesMakeDefault(t *testing.T) {
	_, ts, _ := settingsServer(t)
	code, body := postSettings(t, ts, "/-/voices/default", `{"name":"Companion"}`)
	if code != http.StatusOK || !strings.Contains(body, `"default":"companion"`) {
		t.Fatalf("make default = %d %s", code, body)
	}
	if got := config.DefaultVoice(); got != "companion" {
		t.Errorf("default = %q", got)
	}
	if code, _ := postSettings(t, ts, "/-/voices/default", `{"name":"nope"}`); code != http.StatusNotFound {
		t.Errorf("unknown voice = %d, want 404", code)
	}
	if code, _ := postSettings(t, ts, "/-/voices/default", `{"name":"plainspoken"}`, "Origin", "https://evil.example"); code != http.StatusForbidden {
		t.Errorf("cross-site = %d, want 403", code)
	}
	if got := config.DefaultVoice(); got != "companion" {
		t.Errorf("a refused request changed the default to %q", got)
	}
}

func TestVoicesDuplicate(t *testing.T) {
	_, ts, _ := settingsServer(t)
	code, body := postSettings(t, ts, "/-/voices/duplicate", `{"from":"companion","name":"warm"}`)
	if code != http.StatusOK {
		t.Fatalf("duplicate = %d %s", code, body)
	}
	var out map[string]string
	_ = json.Unmarshal([]byte(body), &out)
	home, _ := os.UserHomeDir()
	if out["name"] != "warm" || out["command"] != "/lathe-voice warm" || out["path"] != filepath.Join(home, ".lathe", "voices", "warm.md") {
		t.Errorf("duplicate = %v", out)
	}
	if v, err := voice.Resolve("warm"); err != nil || v.Builtin {
		t.Errorf("warm = %+v, %v", v, err)
	}
	if code, _ := postSettings(t, ts, "/-/voices/duplicate", `{"from":"companion","name":"warm"}`); code != http.StatusConflict {
		t.Errorf("duplicate onto an existing name = %d, want 409", code)
	}
	if code, body := postSettings(t, ts, "/-/voices/duplicate", `{"from":"companion","name":"Two words"}`); code != http.StatusBadRequest || !strings.Contains(body, "lowercase letters") {
		t.Errorf("bad name = %d %s", code, body)
	}
	if code, _ := postSettings(t, ts, "/-/voices/duplicate", `{"from":"nope","name":"x"}`); code != http.StatusNotFound {
		t.Errorf("unknown source = %d, want 404", code)
	}
	if code, _ := postSettings(t, ts, "/-/voices/duplicate", `{"from":"companion","name":"y"}`, "Origin", "https://evil.example"); code != http.StatusForbidden {
		t.Errorf("cross-site = %d, want 403", code)
	}
}

func TestVoicesDeleteAndUndo(t *testing.T) {
	_, ts, tutDir := settingsServer(t)
	addVoice(t, "terse", "Short.")
	setTutorialVoice(t, tutDir, "terse")
	if code, body := postSettings(t, ts, "/-/voices/default", `{"name":"terse"}`); code != http.StatusOK {
		t.Fatal(code, body)
	}

	if code, _ := postSettings(t, ts, "/-/voices/delete", `{"name":"plainspoken"}`); code != http.StatusBadRequest {
		t.Errorf("deleting a built-in = %d, want 400", code)
	}
	if code, _ := postSettings(t, ts, "/-/voices/delete", `{"name":"terse"}`, "Origin", "https://evil.example"); code != http.StatusForbidden {
		t.Errorf("cross-site delete = %d, want 403", code)
	}

	code, body := postSettings(t, ts, "/-/voices/delete", `{"name":"terse"}`)
	if code != http.StatusOK {
		t.Fatalf("delete = %d %s", code, body)
	}
	var out struct {
		Name       string `json:"name"`
		WasDefault bool   `json:"wasDefault"`
		Default    string `json:"default"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	if out.Name != "terse" || !out.WasDefault || out.Default != config.DefaultVoiceName {
		t.Errorf("delete = %+v", out)
	}
	if _, err := voice.Resolve("terse"); err == nil {
		t.Error("terse still resolves after delete")
	}

	// The tutorial written in it keeps a working byline: the name, no spec.
	page := get(t, ts, "/series/part-01.md")
	header := page[strings.Index(page, `class="article-header"`):]
	if !strings.Contains(header, "terse") || strings.Contains(header, `class="voice-reveal"`) {
		t.Error("byline after deleting its voice should name it without the reveal")
	}

	// Undo rewrites the file and makes it the default again.
	code, body = postSettings(t, ts, "/-/voices/restore", `{"name":"terse"}`)
	if code != http.StatusOK {
		t.Fatalf("restore = %d %s", code, body)
	}
	v, err := voice.Resolve("terse")
	if err != nil || v.Sample() != "Short." {
		t.Errorf("restored terse = %+v, %v", v, err)
	}
	if got := config.DefaultVoice(); got != "terse" {
		t.Errorf("default after undo = %q, want terse", got)
	}
	// One undo per delete.
	if code, _ := postSettings(t, ts, "/-/voices/restore", `{"name":"terse"}`); code != http.StatusGone {
		t.Errorf("second restore = %d, want 410", code)
	}
}

func TestVoiceTrashExpires(t *testing.T) {
	var tr voiceTrash
	tr.put("old", deletedVoice{raw: []byte("x"), at: time.Now().Add(-voiceUndoWindow - time.Second)})
	if _, ok := tr.take("old"); ok {
		t.Error("an entry past the undo window was restorable")
	}
	tr.put("new", deletedVoice{raw: []byte("x"), at: time.Now()})
	if _, ok := tr.take("new"); !ok {
		t.Error("a fresh entry was not restorable")
	}
}

func TestVoicesShowFileReveal(t *testing.T) {
	srv, ts, _ := settingsServer(t)
	addVoice(t, "terse", "")
	n := &revealNative{fakeNative: newFakeNative()}
	srv.SetNative(n)
	page := get(t, ts, "/-/settings?voice=terse")
	if !strings.Contains(page, "data-voice-reveal>Show file</button>") || strings.Contains(page, "Copy file path") {
		t.Error("desktop voices tab should offer Show file, not Copy file path")
	}
	if code, body := postSettings(t, ts, "/-/native/reveal", `{"what":"voices"}`); code != http.StatusNoContent {
		t.Fatalf("reveal voices = %d %s", code, body)
	}
	dir, _ := config.VoicesDir()
	if len(n.revealed) != 1 || n.revealed[0] != dir {
		t.Errorf("revealed %v, want %s", n.revealed, dir)
	}
}

func TestAutoVerifySettingSaves(t *testing.T) {
	_, ts, _ := settingsServer(t)
	if config.LoadSettings().AutoVerify {
		t.Fatal("auto_verify defaults on")
	}
	if code, body := postSettings(t, ts, "/-/settings", `{"auto_verify":true}`); code != http.StatusOK || !strings.Contains(body, `"auto_verify":true`) {
		t.Fatalf("save auto_verify = %d %s", code, body)
	}
	if page := get(t, ts, "/-/settings"); !strings.Contains(page, `data-setting="auto_verify" checked>`) {
		t.Error("auto-verify checkbox not checked after saving")
	}
}
