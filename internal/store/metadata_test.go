package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/store"
)

func TestWriteReadMetadata(t *testing.T) {
	dir := t.TempDir()
	original := &store.Tutorial{
		Slug:    "test-tutorial",
		Title:   "Test Tutorial",
		Topic:   "test tutorial",
		Created: time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
		Status:  store.StatusVerified,
	}

	if err := store.WriteMetadata(dir, original); err != nil {
		t.Fatalf("WriteMetadata() error = %v", err)
	}

	got, err := store.ReadMetadata(dir)
	if err != nil {
		t.Fatalf("ReadMetadata() error = %v", err)
	}
	if got.Slug != original.Slug {
		t.Errorf("Slug = %q, want %q", got.Slug, original.Slug)
	}
	if got.Status != original.Status {
		t.Errorf("Status = %q, want %q", got.Status, original.Status)
	}
}

func TestMetadataRoundTripSources(t *testing.T) {
	dir := t.TempDir()
	srcs := []string{"https://ziglang.org/documentation/master/#comptime", "https://example.com/spec#sec3"}
	tut := &store.Tutorial{
		Slug:    "test-tut",
		Status:  store.StatusUnverified,
		Sources: srcs,
	}
	if err := store.WriteMetadata(dir, tut); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	got, err := store.ReadMetadata(dir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if len(got.Sources) != len(srcs) {
		t.Fatalf("Sources = %v, want %v", got.Sources, srcs)
	}
	for i := range srcs {
		if got.Sources[i] != srcs[i] {
			t.Errorf("Sources[%d] = %q, want %q", i, got.Sources[i], srcs[i])
		}
	}
}

func TestSourcesOmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := store.WriteMetadata(dir, &store.Tutorial{Slug: "t", Status: store.StatusUnverified}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "sources") {
		t.Error("sources should be omitted from JSON when empty")
	}
}

func TestNormalizeSources(t *testing.T) {
	// Trims, drops empties, de-dupes first-seen — but preserves case (URLs are
	// case-sensitive, unlike tags).
	got := store.NormalizeSources([]string{" https://A.com/Path ", "", "https://A.com/Path", "https://b.com"})
	want := []string{"https://A.com/Path", "https://b.com"}
	if len(got) != len(want) {
		t.Fatalf("NormalizeSources = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("NormalizeSources[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if store.NormalizeSources([]string{"", "  "}) != nil {
		t.Error("NormalizeSources of all-empty should be nil (stays omitempty)")
	}
}

func TestReadMetadataNotFound(t *testing.T) {
	_, err := store.ReadMetadata("/nonexistent/path/abc123")
	if err == nil {
		t.Error("ReadMetadata() expected error for missing file, got nil")
	}
}

func TestTutorialIsSeries(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		want  bool
	}{
		{"zero parts", nil, false},
		{"one part", []string{"part-01.md"}, false},
		{"two parts", []string{"part-01.md", "part-02.md"}, true},
		{"three parts", []string{"part-01.md", "part-02.md", "part-03.md"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tut := &store.Tutorial{Parts: tc.parts}
			if got := tut.IsSeries(); got != tc.want {
				t.Errorf("IsSeries() = %v, want %v (parts=%v)", got, tc.want, tc.parts)
			}
		})
	}
}

func TestMetadataRoundTripPendingPart(t *testing.T) {
	dir := t.TempDir()
	tut := &store.Tutorial{
		Slug:        "test-tut",
		Title:       "Test Tutorial",
		Status:      store.StatusExtending,
		Parts:       []string{"part-01.md", "part-02.md", "part-03.md"},
		PendingPart: "part-04.md",
	}
	if err := store.WriteMetadata(dir, tut); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	got, err := store.ReadMetadata(dir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if got.PendingPart != "part-04.md" {
		t.Errorf("PendingPart = %q, want %q", got.PendingPart, "part-04.md")
	}
	if got.Status != store.StatusExtending {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusExtending)
	}
}

func TestStatusExtendingValue(t *testing.T) {
	if store.StatusExtending != "extending" {
		t.Errorf("StatusExtending = %q, want %q", store.StatusExtending, "extending")
	}
}

func TestVerifyResultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	original := &store.VerifyResult{
		Status:     store.StatusFailed,
		Part:       "part-02.md",
		FailedStep: 4,
		Error:      "zig build failed: error: expected ';'",
		CheckedAt:  "2026-06-03T12:00:00Z",
	}
	if err := store.WriteVerifyResult(dir, original); err != nil {
		t.Fatalf("WriteVerifyResult: %v", err)
	}
	got, err := store.ReadVerifyResult(dir)
	if err != nil {
		t.Fatalf("ReadVerifyResult: %v", err)
	}
	if got.Status != original.Status {
		t.Errorf("Status = %q, want %q", got.Status, original.Status)
	}
	if got.Part != original.Part {
		t.Errorf("Part = %q, want %q", got.Part, original.Part)
	}
	if got.FailedStep != original.FailedStep {
		t.Errorf("FailedStep = %d, want %d", got.FailedStep, original.FailedStep)
	}
	if got.Error != original.Error {
		t.Errorf("Error = %q, want %q", got.Error, original.Error)
	}
}

func TestReadVerifyResultNotFound(t *testing.T) {
	if _, err := store.ReadVerifyResult(t.TempDir()); err == nil {
		t.Error("ReadVerifyResult() expected error for missing file, got nil")
	}
}

func TestStatusValues(t *testing.T) {
	cases := []struct {
		status store.Status
		want   string
	}{
		{store.StatusUnverified, "unverified"},
		{store.StatusVerifying, "verifying"},
		{store.StatusVerified, "verified"},
		{store.StatusFailed, "failed"},
		{store.StatusSkipped, "skipped"},
		{store.StatusExtending, "extending"},
	}
	for _, c := range cases {
		if string(c.status) != c.want {
			t.Errorf("status = %q, want %q", c.status, c.want)
		}
	}
}

func TestPendingPartOmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	tut := &store.Tutorial{
		Slug:   "test-tut",
		Status: store.StatusVerified,
	}
	if err := store.WriteMetadata(dir, tut); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "pending_part") {
		t.Error("pending_part should be omitted from JSON when empty")
	}
}
