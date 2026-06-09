package serve

import (
	"math"

	"github.com/devenjarvis/lathe/internal/store"
)

type CardProgress struct {
	IsSeries   bool
	Percent    int
	PartNumber int
	TotalParts int
	Segments   []CardProgressSegment
}

type CardProgressSegment struct {
	Number   int
	Complete bool
	Current  bool
}

func cardProgress(tut *store.Tutorial) *CardProgress {
	if tut.Progress == nil {
		return nil
	}
	if !tut.IsSeries() {
		return &CardProgress{Percent: percent(tut.Progress.Ratio)}
	}
	for i, part := range tut.Parts {
		if part != tut.Progress.Part {
			continue
		}
		partNumber := i + 1
		segments := make([]CardProgressSegment, len(tut.Parts))
		for j := range tut.Parts {
			number := j + 1
			segments[j] = CardProgressSegment{
				Number:   number,
				Complete: number < partNumber,
				Current:  number == partNumber,
			}
		}
		return &CardProgress{
			IsSeries:   true,
			PartNumber: partNumber,
			TotalParts: len(tut.Parts),
			Segments:   segments,
		}
	}
	return nil
}

func percent(progress float64) int {
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	return int(math.Round(progress * 100))
}
