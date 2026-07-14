package sync

import "math"

// SyncableNumbers filters a series' chapter numbers down to the ones sync
// may ever act on. Umbrella spec §6 / phase-4 spec §2(c): "unparseable
// chapters (number == -1/unrecognized) are FILTERED out of all sync" — the
// chapter normaliser's unparseable sentinel is -1 (internal/chapter), and
// any other negative or NaN value is treated the same way (defensively —
// a sync-side corruption guard, not a chapter-domain concern). The result
// preserves the input order; it does not sort.
func SyncableNumbers(chapterNumbers []float64) []float64 {
	out := make([]float64, 0, len(chapterNumbers))
	for _, n := range chapterNumbers {
		if !isSyncableNumber(n) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// isSyncableNumber reports whether n is a real, non-negative chapter number
// eligible for tracker sync — false for the unparseable sentinel (-1), any
// other negative value, and NaN.
func isSyncableNumber(n float64) bool {
	return !math.IsNaN(n) && n >= 0
}

// MarkReadUpTo walks sortedChapterNumbers (expected caller-supplied in
// ascending chapter order, already passed through SyncableNumbers) and
// counts how many LEADING chapters to mark read from a tracker's reported
// remoteLastRead. It implements phase-4 spec §2(c)'s "mark local read from
// remote" walk: a chapter counts only while the walk is BOTH monotonically
// increasing (strictly greater than the previous counted chapter) AND at or
// below remoteLastRead. The walk STOPS — permanently, for the rest of the
// slice — at the first entry that is not strictly greater than the last
// counted chapter (a re-descending or duplicate number, e.g. numbering
// corruption like "Vol 2 Ch 1" reusing an earlier chapter number). This is
// what lets local read-state survive numbering corruption instead of
// blindly re-marking every chapter <= remoteLastRead regardless of order.
func MarkReadUpTo(sortedChapterNumbers []float64, remoteLastRead float64) (readCount int) {
	prev := math.Inf(-1)
	for _, n := range sortedChapterNumbers {
		if n <= prev {
			// Non-monotonic: a repeat or a re-descend. Corruption — stop the
			// walk here, never resume past it even if a later number would
			// otherwise qualify.
			break
		}
		if n > remoteLastRead {
			// Past what the tracker reports read. Because the walk is
			// monotonically increasing, no later entry can be <=
			// remoteLastRead either, so there is nothing left to count.
			break
		}
		prev = n
		readCount++
	}
	return readCount
}
