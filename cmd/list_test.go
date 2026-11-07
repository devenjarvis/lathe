package cmd

import (
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

func TestStatusBadgeWriting(t *testing.T) {
	if got := statusBadge(store.StatusWriting); got != "⏳ writing" {
		t.Errorf("statusBadge(writing) = %q", got)
	}
}
