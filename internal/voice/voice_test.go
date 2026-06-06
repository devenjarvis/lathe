package voice_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/voice"
)

// withTempHome points os.UserHomeDir at a temp dir so custom-voice tests never
// touch the developer's real ~/.lathe/voices.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

func TestListIncludesBuiltins(t *testing.T) {
	withTempHome(t)
	voices, err := voice.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]bool{}
	for _, v := range voices {
		got[v.Name] = v.Builtin
	}
	for _, name := range []string{"plainspoken", "companion"} {
		builtin, ok := got[name]
		if !ok {
			t.Errorf("List missing built-in %q", name)
		}
		if !builtin {
			t.Errorf("%q should be marked Builtin", name)
		}
	}
}

func TestResolveBuiltin(t *testing.T) {
	withTempHome(t)
	v, err := voice.Resolve("plainspoken")
	if err != nil {
		t.Fatalf("Resolve plainspoken: %v", err)
	}
	if !v.Builtin || v.Name != "plainspoken" {
		t.Errorf("got %+v, want builtin plainspoken", v)
	}
	if v.Description == "" {
		t.Errorf("expected a description from frontmatter")
	}
}

func TestResolveIsCaseInsensitive(t *testing.T) {
	withTempHome(t)
	if _, err := voice.Resolve("Companion"); err != nil {
		t.Errorf("Resolve should be case-insensitive: %v", err)
	}
}

func TestResolveUnknownErrors(t *testing.T) {
	withTempHome(t)
	if _, err := voice.Resolve("does-not-exist"); err == nil {
		t.Errorf("expected error resolving unknown voice")
	}
}

func TestWrappedHasPreamble(t *testing.T) {
	withTempHome(t)
	v, err := voice.Resolve("plainspoken")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	w := v.Wrapped()
	if !strings.HasPrefix(w, voice.Preamble) {
		t.Errorf("Wrapped output must start with the guardrail preamble")
	}
	// The preamble must carry the load-bearing guardrail language.
	for _, frag := range []string{"tone and register only", "No impersonation", "authored by an LLM"} {
		if !strings.Contains(w, frag) {
			t.Errorf("Wrapped output missing guardrail fragment %q", frag)
		}
	}
	// Frontmatter is stripped from the body.
	if strings.Contains(v.Body(), "description:") {
		t.Errorf("Body should have frontmatter stripped")
	}
}

func TestAddCustomThenResolveAndList(t *testing.T) {
	withTempHome(t)
	spec := "---\nname: terse\ndescription: As few words as possible.\n---\n\n# Terse\n\nUse the fewest words that still teach.\n"
	if err := voice.Add("terse", []byte(spec)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	v, err := voice.Resolve("terse")
	if err != nil {
		t.Fatalf("Resolve custom: %v", err)
	}
	if v.Builtin {
		t.Errorf("custom voice should not be marked Builtin")
	}
	if !strings.HasPrefix(v.Wrapped(), voice.Preamble) {
		t.Errorf("custom voice must also be wrapped with the preamble")
	}
	// It shows up in List.
	voices, err := voice.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, lv := range voices {
		if lv.Name == "terse" {
			found = true
		}
	}
	if !found {
		t.Errorf("List should include the custom voice")
	}
}

func TestAddRejectsBuiltinCollision(t *testing.T) {
	withTempHome(t)
	if err := voice.Add("companion", []byte("---\nname: companion\n---\nhi\n")); err == nil {
		t.Errorf("Add must reject a name that collides with a built-in")
	}
	if err := voice.Add("plainspoken", []byte("x")); err == nil {
		t.Errorf("Add must reject overriding the default built-in")
	}
}

func TestAddRejectsInvalidNames(t *testing.T) {
	withTempHome(t)
	for _, bad := range []string{"", "..", "a/b", `a\b`, "a\x00b"} {
		if err := voice.Add(bad, []byte("x")); err == nil {
			t.Errorf("Add(%q) should be rejected", bad)
		}
	}
}

func TestRemoveCustomAndRefuseBuiltin(t *testing.T) {
	home := withTempHome(t)
	spec := "---\nname: temp\n---\nbody\n"
	if err := voice.Add("temp", []byte(spec)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := voice.Remove("temp"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".lathe", "voices", "temp.md")); !os.IsNotExist(err) {
		t.Errorf("custom voice file should be gone after Remove")
	}
	if err := voice.Remove("temp"); err == nil {
		t.Errorf("Remove of a missing voice should error")
	}
	if err := voice.Remove("companion"); err == nil {
		t.Errorf("Remove must refuse a built-in")
	}
}

func TestIsBuiltin(t *testing.T) {
	if !voice.IsBuiltin("plainspoken") || !voice.IsBuiltin("Companion") {
		t.Errorf("IsBuiltin should recognize presets case-insensitively")
	}
	if voice.IsBuiltin("nope") {
		t.Errorf("IsBuiltin should be false for unknown names")
	}
}
