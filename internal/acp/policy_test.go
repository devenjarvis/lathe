package acp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPolicyPaths(t *testing.T) {
	root := t.TempDir()
	tut := filepath.Join(root, "tut")
	scratch := filepath.Join(root, "scratch")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{tut, scratch, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A link inside the tutorial that points outside it.
	if err := os.Symlink(outside, filepath.Join(tut, "escape")); err != nil {
		t.Fatal(err)
	}
	// A dangling link: writing through it would land outside.
	if err := os.Symlink(filepath.Join(outside, "new"), filepath.Join(scratch, "dangling")); err != nil {
		t.Fatal(err)
	}

	p := VerifyPolicy(tut, scratch)
	for _, tc := range []struct {
		name  string
		check func(string) (string, error)
		path  string
		ok    bool
	}{
		{"read tutorial file", p.CheckRead, filepath.Join(tut, "part-01.md"), true},
		{"read tutorial root", p.CheckRead, tut, true},
		{"read outside", p.CheckRead, filepath.Join(outside, "secret"), false},
		{"read dotdot escape", p.CheckRead, filepath.Join(tut, "..", "outside", "secret"), false},
		{"read sibling prefix", p.CheckRead, tut + "-other/file", false},
		{"read through symlink", p.CheckRead, filepath.Join(tut, "escape", "secret"), false},
		{"read relative", p.CheckRead, "part-01.md", false},
		{"write scratch new nested", p.CheckWrite, filepath.Join(scratch, "a", "b", "main.go"), true},
		{"write tutorial", p.CheckWrite, filepath.Join(tut, "part-01.md"), false},
		{"write dangling symlink", p.CheckWrite, filepath.Join(scratch, "dangling"), false},
		{"exec scratch", p.CheckExec, filepath.Join(scratch, "sub"), true},
		{"exec default", p.CheckExec, "", true},
		{"exec tutorial", p.CheckExec, tut, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.check(tc.path)
			if tc.ok && err != nil {
				t.Errorf("want allowed, got %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrOutsidePolicy) {
				t.Errorf("want ErrOutsidePolicy, got %v", err)
			}
		})
	}
}

func TestJobPolicies(t *testing.T) {
	root := t.TempDir()
	tut, scratch, ws := filepath.Join(root, "tut"), filepath.Join(root, "scratch"), filepath.Join(root, "ws")
	ask := AskPolicy(tut)
	if _, err := ask.CheckRead(filepath.Join(tut, "part-01.md")); err != nil {
		t.Errorf("ask read: %v", err)
	}
	if _, err := ask.CheckWrite(filepath.Join(tut, "part-01.md")); !errors.Is(err, ErrOutsidePolicy) {
		t.Errorf("ask write = %v, want refused", err)
	}
	if _, err := ask.CheckExec(tut); !errors.Is(err, ErrOutsidePolicy) {
		t.Errorf("ask exec = %v, want refused", err)
	}

	ext := ExtendPolicy(tut, scratch)
	if _, err := ext.CheckWrite(filepath.Join(tut, "part-03.md")); err != nil {
		t.Errorf("extend write tutorial: %v", err)
	}
	if _, err := ext.CheckExec(tut); !errors.Is(err, ErrOutsidePolicy) {
		t.Errorf("extend exec in tutorial = %v, want refused", err)
	}

	chk := CheckPolicy(tut, ws, scratch)
	if _, err := chk.CheckRead(filepath.Join(ws, "main.go")); err != nil {
		t.Errorf("check read workspace: %v", err)
	}
	if _, err := chk.CheckWrite(filepath.Join(ws, "main.go")); !errors.Is(err, ErrOutsidePolicy) {
		t.Errorf("check write workspace = %v, want refused", err)
	}
	if _, err := chk.CheckExec(ws); !errors.Is(err, ErrOutsidePolicy) {
		t.Errorf("check exec in workspace = %v, want refused", err)
	}

	voiceFile := filepath.Join(root, "voices", "terse.md")
	nw := NewPolicy(scratch, ws, voiceFile)
	for _, ok := range []string{filepath.Join(scratch, "slug", "part-01.md"), filepath.Join(ws, "go.mod"), voiceFile} {
		if _, err := nw.CheckRead(ok); err != nil {
			t.Errorf("new read %s: %v", ok, err)
		}
	}
	for _, no := range []string{filepath.Join(tut, "part-01.md"), filepath.Join(root, "voices", "other.md")} {
		if _, err := nw.CheckRead(no); !errors.Is(err, ErrOutsidePolicy) {
			t.Errorf("new read %s = %v, want refused", no, err)
		}
	}
	if _, err := nw.CheckWrite(filepath.Join(scratch, "slug", "part-01.md")); err != nil {
		t.Errorf("new write scratch: %v", err)
	}
	for _, no := range []string{filepath.Join(ws, "main.go"), filepath.Join(tut, "part-01.md"), voiceFile} {
		if _, err := nw.CheckWrite(no); !errors.Is(err, ErrOutsidePolicy) {
			t.Errorf("new write %s = %v, want refused", no, err)
		}
	}
	if _, err := nw.CheckExec(ws); !errors.Is(err, ErrOutsidePolicy) {
		t.Errorf("new exec in workspace = %v, want refused", err)
	}
	if _, err := nw.CheckExec(""); err != nil {
		t.Errorf("new exec in scratch: %v", err)
	}
	if bare := NewPolicy(scratch, "", ""); len(bare.Read) != 1 {
		t.Errorf("NewPolicy without workspace or voice reads %v, want only the scratch dir", bare.Read)
	}

	vp := VoicePolicy()
	if _, err := vp.CheckRead(voiceFile); !errors.Is(err, ErrOutsidePolicy) {
		t.Errorf("voice read = %v, want refused", err)
	}
	if _, err := vp.CheckWrite(voiceFile); !errors.Is(err, ErrOutsidePolicy) {
		t.Errorf("voice write = %v, want refused", err)
	}
	if _, err := vp.CheckExec(""); !errors.Is(err, ErrOutsidePolicy) {
		t.Errorf("voice exec = %v, want refused", err)
	}

	var none *Policy
	if _, err := none.CheckRead(filepath.Join(tut, "x")); !errors.Is(err, ErrOutsidePolicy) {
		t.Errorf("nil policy read = %v, want refused", err)
	}
}
