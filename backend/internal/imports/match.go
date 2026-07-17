// Package imports groups multi-source manga search results into logical series
// and provides the import workflow for adopting library entries into Tsundoku.
package imports

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// titleMatchThreshold is the maximum normalised Levenshtein distance at which two
// titles are considered the same series. Ported from Kaizoku.GO search.go:500.
const titleMatchThreshold = 0.1

// Candidate is one source's search hit, fed to the matcher.
type Candidate struct {
	// Source is the Suwayomi source ID.
	Source string
	// SourceName is the human-readable source name (e.g. "MangaDex EN").
	SourceName string
	// Lang is the content language code (e.g. "en", "ko").
	Lang string
	// MangaID is the Suwayomi-internal manga ID for this source.
	MangaID int
	// Title is the raw title as returned by the source.
	Title string
	// URL is the provider-canonical ADDRESSING url for this manga — what every
	// adopt/add-source/match request sends back to identify it. NOT a
	// clickable browser link; see RealURL.
	URL string
	// RealURL is the fully-qualified, browser-clickable URL for this manga
	// (Mihon's HttpSource.getMangaUrl); powers the "View on source" external
	// link. Distinct from URL — never used for addressing. Empty when the
	// engine host could not resolve one.
	RealURL string
	// ThumbnailURL is the Tsundoku-relative cover proxy path
	// ("/api/sources/{source}/manga/{mangaId}/cover"), or "" when the source
	// provided no thumbnail at all (see thumbnailProxyPath in service.go).
	ThumbnailURL string
	// Author is the manga's writing credit; "" when the source omits it.
	Author string
	// Artist is the manga's art credit; "" when the source omits it.
	Artist string
	// Description is the synopsis/summary text; "" when the source omits it.
	Description string
	// Genres is the source's genre/tag list; never nil (empty slice when the
	// source provides none), so the DTO always serialises "genres": [] not null.
	Genres []string
}

// Group is one logical series and all the per-source candidates that matched it.
type Group struct {
	// Title is the representative title — the longest member's raw Title.
	Title string
	// Candidates holds every source hit that belongs to this logical series.
	Candidates []Candidate
}

// reParenthetical matches a parenthetical annotation — optional leading
// whitespace followed by a balanced (...) group — used to strip variant tags
// like "(Official)", "(KR)", "(Season 2)" from titles before comparison.
var reParenthetical = regexp.MustCompile(`\s*\([^)]*\)`)

// reNonAlphanumSpace matches any character that is not a lowercase letter,
// ASCII digit, or space — used to strip remaining punctuation after lowercasing.
var reNonAlphanumSpace = regexp.MustCompile(`[^a-z0-9 ]+`)

// reLeadingArticle matches a leading "the", "a", or "an" followed by at least
// one space, anchored at the start of the string.
var reLeadingArticle = regexp.MustCompile(`^(the|a|an) +`)

// reCollapseSpace matches runs of two or more spaces for whitespace collapsing.
var reCollapseSpace = regexp.MustCompile(` {2,}`)

// normalizeTitle prepares a raw manga title for fuzzy comparison:
//  1. Lowercase the entire string.
//  2. Strip parenthetical variant annotations like "(Official)", "(KR)".
//  3. Strip a single leading article ("the", "a", "an") as a whole word.
//  4. Remove every character that is not [a-z0-9 ].
//  5. Collapse runs of whitespace to a single space and trim ends.
func normalizeTitle(s string) string {
	// Lowercase first so article and parenthetical matching is case-insensitive.
	s = strings.ToLower(s)
	// Remove parenthetical annotations before article stripping so a title like
	// "The (Unofficial) Solo Leveling" still has its article stripped correctly.
	s = reParenthetical.ReplaceAllString(s, "")
	// Strip leading article only when it appears as a complete first word.
	s = reLeadingArticle.ReplaceAllString(s, "")
	// Drop all non-alphanumeric, non-space characters (punctuation, symbols, Unicode).
	s = reNonAlphanumSpace.ReplaceAllString(s, "")
	// Collapse internal whitespace and remove leading/trailing spaces.
	s = reCollapseSpace.ReplaceAllString(s, " ")
	return strings.TrimFunc(s, unicode.IsSpace)
}

// levenshtein returns the edit distance between two rune slices using the
// classic two-row dynamic-programming algorithm. Operating on runes ensures
// non-ASCII manga titles (e.g. Korean, Chinese) count correctly by character,
// not by byte.
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

// areSimilar reports whether two raw titles refer to the same series.
// It normalises both titles then computes the Levenshtein distance divided by
// the length of the longer title (in runes). Returns false when both normalised
// titles are empty, so that blank results do not spuriously group together.
func areSimilar(a, b string) bool {
	na, nb := normalizeTitle(a), normalizeTitle(b)

	ra, rb := []rune(na), []rune(nb)
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}
	// Both empty — treat as non-similar to avoid merging placeholder results.
	if maxLen == 0 {
		return false
	}

	dist := levenshtein(ra, rb)
	return float64(dist)/float64(maxLen) <= titleMatchThreshold
}

// unionFind is a small path-compressing union-find over integer indices.
type unionFind struct {
	parent []int
}

// newUnionFind returns a union-find over n singleton elements.
func newUnionFind(n int) unionFind {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return unionFind{parent: p}
}

// find returns the root of the component containing i, with path compression.
func (u *unionFind) find(i int) int {
	if u.parent[i] != i {
		u.parent[i] = u.find(u.parent[i])
	}
	return u.parent[i]
}

// union merges the components of i and j.
func (u *unionFind) union(i, j int) {
	ri, rj := u.find(i), u.find(j)
	if ri != rj {
		u.parent[ri] = rj
	}
}

// groupCandidates clusters a flat list of per-source search hits into logical
// series using union-find over pairwise title similarity. The representative
// title for each group is the longest raw Title among its members.
//
// Complexity: O(n²) pairwise comparisons — acceptable because search result
// sets are small (typically < 50 candidates across all sources).
func groupCandidates(in []Candidate) []Group {
	n := len(in)
	if n == 0 {
		return []Group{}
	}

	uf := newUnionFind(n)

	// Pairwise similarity check — O(n²).
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if areSimilar(in[i].Title, in[j].Title) {
				uf.union(i, j)
			}
		}
	}

	// Gather candidates by their root, preserving insertion order within each group.
	rootIndex := make(map[int]int) // root → index in groups slice
	groups := []Group{}

	for i, c := range in {
		root := uf.find(i)
		idx, exists := rootIndex[root]
		if !exists {
			idx = len(groups)
			rootIndex[root] = idx
			groups = append(groups, Group{})
		}
		g := &groups[idx]
		g.Candidates = append(g.Candidates, c)
		// Keep the longest raw Title as the group's representative.
		if len([]rune(c.Title)) > len([]rune(g.Title)) {
			g.Title = c.Title
		}
	}

	return groups
}

// Relevance score tier bases. A group's score is the BEST (lowest) member score;
// the tiers are disjoint bands so a "contains"/"prefix" match always outranks a
// loose fuzzy match regardless of raw edit distance (a longer title that
// contains the query is a strong match even though its edit distance is large).
// Within a tier the fractional part is the normalised Levenshtein distance, so
// closer matches still sort ahead of looser ones in the same tier. Lower = better.
const (
	scorePrefix   = 1.0 // query is a prefix of a member title (or vice-versa)
	scoreContains = 2.0 // query is a substring of a member title (or vice-versa)
	scoreFuzzy    = 3.0 // neither contains the other — pure fuzzy distance
	scoreNoMatch  = 4.0 // either side normalises to empty (nothing to compare)
)

// relevanceScore rates how well a single candidate title answers the search
// query. Both strings are normalised via normalizeTitle (reused, never
// reimplemented); the comparison is:
//
//	exact normalised equality        -> 0.0            (best)
//	one is a prefix of the other     -> [scorePrefix,   scorePrefix+1)
//	one is a substring of the other  -> [scoreContains, scoreContains+1)
//	otherwise (fuzzy)                -> [scoreFuzzy,     scoreFuzzy+1)
//	either side empty                -> scoreNoMatch    (worst)
//
// The fractional part is the normalised Levenshtein distance (edit distance over
// the longer length, in [0,1)), so within a tier a closer match ranks first.
// Lower = better.
func relevanceScore(query, title string) float64 {
	nq, nt := normalizeTitle(query), normalizeTitle(title)
	if nq == "" || nt == "" {
		return scoreNoMatch
	}
	if nq == nt {
		return 0.0
	}

	ra, rb := []rune(nq), []rune(nt)
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}
	nd := float64(levenshtein(ra, rb)) / float64(maxLen) // in (0,1)

	switch {
	case strings.HasPrefix(nt, nq) || strings.HasPrefix(nq, nt):
		return scorePrefix + nd
	case strings.Contains(nt, nq) || strings.Contains(nq, nt):
		return scoreContains + nd
	default:
		return scoreFuzzy + nd
	}
}

// groupScore is a group's relevance to the query: the BEST (minimum)
// relevanceScore across its member candidates. A single strongly-matching source
// is enough to float a group to the top even if its other members are weaker.
func groupScore(query string, g Group) float64 {
	best := scoreNoMatch
	for _, c := range g.Candidates {
		if sc := relevanceScore(query, c.Title); sc < best {
			best = sc
		}
	}
	return best
}

// rankGroups sorts groups by relevance to query, best match first, IN PLACE.
// Ordering (deterministic):
//  1. best (lowest) group relevance score — see groupScore / relevanceScore;
//  2. tiebreak: more candidates first (broader cross-source agreement);
//  3. tiebreak: group title alphabetical (stable, reproducible ordering).
//
// This is the fix for the post-engine-switch regression where the engine returns
// many more, source-unranked results and groupCandidates preserved arbitrary
// goroutine-completion insertion order, burying the correct match. O(groups ×
// members) scoring — fine for interactive search result sizes.
func rankGroups(query string, groups []Group) {
	// Pair each group with its score up front. Scoring must NOT be keyed by slice
	// position: sort.Slice's less-func receives CURRENT positions, which move as it
	// swaps, so a position-keyed score map would compare the wrong elements.
	type scored struct {
		group Group
		score float64
	}
	pairs := make([]scored, len(groups))
	for i := range groups {
		pairs[i] = scored{group: groups[i], score: groupScore(query, groups[i])}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score < pairs[j].score
		}
		if len(pairs[i].group.Candidates) != len(pairs[j].group.Candidates) {
			return len(pairs[i].group.Candidates) > len(pairs[j].group.Candidates)
		}
		return pairs[i].group.Title < pairs[j].group.Title
	})
	for i := range pairs {
		groups[i] = pairs[i].group
	}
}
