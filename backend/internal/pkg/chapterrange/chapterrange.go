// Package chapterrange collapses a set of chapter numbers into contiguous runs and renders them as
// a human-readable coverage string ("1-90, 92-101"). It is the ONE home for that logic: the import
// wizard uses it for a source's LIVE per-scanlator breakdown, and the series domain uses it for a
// provider's STORED ProviderChapter feed. It lives in pkg/ (a pure, dependency-free kernel, like
// pkg/urlx) so neither domain has to import the other — series must never depend on the
// import-workflow domain, and a shared copy in both would be the DRY violation this package exists
// to prevent.
package chapterrange

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// Range is one contiguous run of chapter numbers, inclusive of both ends. A single-chapter run has
// From == To. Exported so a later DTO/endpoint can surface the structured coverage alongside (or
// instead of) the formatted display string.
type Range struct {
	From float64
	To   float64
}

// gapThreshold is the maximum distance between two consecutive (sorted) chapter numbers that still
// counts as "the same run". It is deliberately 1.1, not 1.0: chapters are sometimes numbered with
// fractional steps (e.g. an extra ".5" chapter between two integers), and a strict 1.0 threshold
// would spuriously split those into their own single-chapter runs. Ported byte-for-byte from
// Kaizoku.GO's proven formatChapterRanges/formatRange/formatNum (internal/handler/search.go).
const gapThreshold = 1.1

// ChapterRanges sorts numbers ascending and collapses them into contiguous runs: while the next
// number is within gapThreshold of the current run's end, it extends the run; a bigger jump closes
// the run and starts a new one. This is the single core walk — FormatChapterRanges is built on top
// of it so the run-collapsing logic exists in exactly one place.
//
// Empty input returns nil (not an empty non-nil slice) — there is no coverage to report.
func ChapterRanges(numbers []float64) []Range {
	if len(numbers) == 0 {
		return nil
	}

	sorted := make([]float64, len(numbers))
	copy(sorted, numbers)
	sort.Float64s(sorted)

	var ranges []Range
	start := sorted[0]
	end := sorted[0]

	for _, n := range sorted[1:] {
		if n-end <= gapThreshold {
			end = n
			continue
		}
		ranges = append(ranges, Range{From: start, To: end})
		start = n
		end = n
	}
	ranges = append(ranges, Range{From: start, To: end})

	return ranges
}

// FormatChapterRanges renders numbers as a human-readable coverage string, e.g. "1-90, 92-101".
// It is implemented purely in terms of ChapterRanges plus a formatter for each run, so the
// gap-collapsing walk is never duplicated between the structured and display forms.
//
// Each run formats as "start" when start == end, else "start-end"; each number formats as a plain
// integer when whole (e.g. "90") or to one decimal place when fractional (e.g. "10.5"). Runs are
// joined with ", ". Empty input returns "".
func FormatChapterRanges(numbers []float64) string {
	ranges := ChapterRanges(numbers)
	if len(ranges) == 0 {
		return ""
	}

	formatted := make([]string, len(ranges))
	for i, r := range ranges {
		formatted[i] = formatRange(r)
	}

	return strings.Join(formatted, ", ")
}

// formatRange renders a single Range as "start" (single chapter) or "start-end" (a span).
func formatRange(r Range) string {
	if r.From == r.To {
		return formatNum(r.From)
	}
	return formatNum(r.From) + "-" + formatNum(r.To)
}

// formatNum renders a chapter number as a plain integer when it has no fractional part, or to one
// decimal place otherwise (e.g. 90 -> "90", 10.5 -> "10.5").
func formatNum(n float64) string {
	if n == math.Floor(n) {
		return strconv.Itoa(int(n))
	}
	return strconv.FormatFloat(n, 'f', 1, 64)
}
