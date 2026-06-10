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
		// Mirror the series branch: don't render progress for a part that no
		// longer exists (e.g. a legacy index.md promoted to part-01.md, or a
		// re-store that changed the part set), which would otherwise show a bar
		// for content the reader can't reach.
		if !isKnownPart(tut, tut.Progress.Part) {
			return nil
		}
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
	return int(math.Round(clampRatio(progress) * 100))
}
