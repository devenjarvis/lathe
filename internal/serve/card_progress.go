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
	Percent int
	Current bool
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
		currentPercent := percent(tut.Progress.Ratio)
		segments := make([]CardProgressSegment, len(tut.Parts))
		for j := range tut.Parts {
			number := j + 1
			segmentPercent := 0
			if number < partNumber {
				segmentPercent = 100
			} else if number == partNumber {
				segmentPercent = currentPercent
			}
			segments[j] = CardProgressSegment{
				Percent: segmentPercent,
				Current: number == partNumber,
			}
		}
		return &CardProgress{
			IsSeries:   true,
			Percent:    currentPercent,
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
