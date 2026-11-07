package serve

import (
	"testing"

	"github.com/devenjarvis/lathe/internal/store"
)

// A New tutorial reservation (status writing) is grouped as Being written,
// not Not started.
func TestStatusGroupWriting(t *testing.T) {
	tut := &store.Tutorial{}
	tut.Status = store.StatusWriting
	if got := statusGroup(tut); got != "writing" {
		t.Errorf("statusGroup(writing) = %q, want writing", got)
	}
	cards := groupCards([]*libCard{{Tutorial: tut}}, groupStatus)
	if len(cards) != 1 || cards[0].Label != "Being written" {
		t.Errorf("groups = %+v, want one Being written group", cards)
	}
}
