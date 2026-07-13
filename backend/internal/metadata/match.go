package metadata

import "strings"

// MatchType ranks how confidently a candidate title matched a MatchQuery.
// The ordinal ordering (None < Closest < Exact) is load-bearing —
// NameSimilarity compares MatchType values directly to keep the best result
// across multiple query titles.
type MatchType int

const (
	// MatchNone means no query title was within its length bucket's
	// distance threshold of the candidate.
	MatchNone MatchType = iota
	// MatchClosest means a query title was within threshold but not an
	// exact (case-insensitive) match.
	MatchClosest
	// MatchExact means a query title equals the candidate once both are
	// uppercased.
	MatchExact
)

// distanceThreshold returns the maximum Levenshtein distance still counted
// as a "closest" match for a compared-string length of the given size — the
// LONGER of the two strings being compared. Ported from Kaizoku.GO's
// length-scaled fuzzy-match rule: short titles must match near-exactly,
// longer titles tolerate more edits.
func distanceThreshold(length int) int {
	switch {
	case length <= 3:
		return 0
	case length <= 6:
		return 1
	case length <= 9:
		return 2
	default:
		return 3
	}
}

// compareTitle scores one (query title, candidate) pair: MatchExact when
// the uppercased strings are identical, MatchClosest when the Levenshtein
// distance is within the longer string's length-bucket threshold, else
// MatchNone.
func compareTitle(title, candidate string) MatchType {
	a := []rune(strings.ToUpper(title))
	b := []rune(strings.ToUpper(candidate))

	dist := levenshtein(a, b)
	if dist == 0 {
		return MatchExact
	}

	longer := len(a)
	if len(b) > longer {
		longer = len(b)
	}
	if dist <= distanceThreshold(longer) {
		return MatchClosest
	}
	return MatchNone
}

// NameSimilarity compares candidate against query.Title and every entry in
// query.AltTitles (uppercased, via Levenshtein distance under a
// length-scaled threshold — see distanceThreshold) and returns the BEST
// match across all of them: MatchExact if any comparison is an exact
// (case-insensitive) match, else MatchClosest if any is within threshold,
// else MatchNone. Blank query titles are skipped so they can never win a
// spurious match.
func NameSimilarity(query MatchQuery, candidate string) MatchType {
	best := MatchNone

	consider := func(title string) {
		if title == "" {
			return
		}
		if m := compareTitle(title, candidate); m > best {
			best = m
		}
	}

	consider(query.Title)
	for _, alt := range query.AltTitles {
		consider(alt)
	}

	return best
}

// levenshtein returns the edit distance between two rune slices using the
// classic two-row dynamic-programming algorithm, operating on runes so
// non-ASCII titles (Korean, Chinese, ...) count by character, not by byte.
// This mirrors internal/imports/match.go's levenshtein (which stays
// unexported there); it is re-implemented here rather than extracted to a
// shared package because this task's scope is limited to internal/metadata
// (see backend/CLAUDE.md — imports is a sibling domain, not touched here).
func levenshtein(a, b []rune) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	// prev holds the costs for the previous row; curr for the current row.
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	// Initialise: cost of transforming empty prefix of a into each prefix of b.
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i // cost of deleting all of a[0..i-1]
		for j := 1; j <= lb; j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1] // characters match — no cost
			} else {
				// Minimum of: delete from a, insert into a, substitute.
				curr[j] = 1 + min(prev[j], curr[j-1], prev[j-1])
			}
		}
		prev, curr = curr, prev // swap rows; prev now holds the completed row
	}

	return prev[lb]
}
