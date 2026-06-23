package skills

import (
	"strings"
	"testing"
)

func TestAllReturnsEverySkillWithMetadata(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatalf("All() error: %v", err)
	}
	const want = 7
	if len(all) != want {
		t.Fatalf("All() returned %d skills, want %d", len(all), want)
	}

	wantSlugs := map[string]bool{
		"lathe":        false,
		"lathe-ask":    false,
		"lathe-extend": false,
		"lathe-tag":    false,
		"lathe-verify": false,
		"lathe-voice":  false,
		"lathe-work":   false,
	}
	for _, s := range all {
		if _, ok := wantSlugs[s.Slug]; !ok {
			t.Errorf("unexpected slug %q", s.Slug)
			continue
		}
		wantSlugs[s.Slug] = true
		if s.Name == "" {
			t.Errorf("skill %q has empty name", s.Slug)
		}
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("skill %q has empty description", s.Slug)
		}
		if len(s.Raw) == 0 {
			t.Errorf("skill %q has empty raw bytes", s.Slug)
		}
	}
	for slug, found := range wantSlugs {
		if !found {
			t.Errorf("missing expected skill %q", slug)
		}
	}
}
