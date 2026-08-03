package cmd

import (
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func TestStatusBadge(t *testing.T) {
	tests := []struct {
		status store.Status
		want   string
	}{
		{store.StatusUnverified, ""}, // calm default — no badge, matching the web UI
		{store.StatusVerified, "verified"},
		{store.StatusVerifying, "verifying"},
		{store.StatusFailed, "failed"},
		{store.StatusSkipped, "skipped"},
		{store.StatusExtending, "extending"},
		{store.StatusStale, "stale"},
	}
	for _, tt := range tests {
		got := statusBadge(tt.status)
		if tt.want == "" {
			if got != "" {
				t.Errorf("statusBadge(%q) = %q, want empty", tt.status, got)
			}
			continue
		}
		if !strings.Contains(got, tt.want) {
			t.Errorf("statusBadge(%q) = %q, want it to contain %q", tt.status, got, tt.want)
		}
	}
}
