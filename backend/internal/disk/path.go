// Package disk implements the Komga-faithful disk renderer for Tsundoku.
//
// It writes CBZ archives (with ComicInfo.xml provenance) at a categorized
// library layout and maintains a per-series tsundoku.json sidecar. The layout
// is byte-identical to the Kaizoku.GO contract so that Komga reads the library
// without reconfiguration and Task 7 can reconstruct the database from disk.
package disk

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// invalidPathCharMap replaces filesystem-illegal characters with visually
// similar Unicode lookalikes, matching the .NET Kaizoku behaviour exactly.
// The full map must be ported verbatim — do not add, remove, or reorder entries.
var invalidPathCharMap = [][2]string{
	{"*", "★"},  // ★  Black Star
	{"|", "¦"},  // ¦  Broken Bar
	{"\\", "⧹"}, // ⟹  Big Reverse Solidus
	{"/", "⁄"},  // ⁄  Fraction Slash
	{":", "։"},  // ։  Armenian Full Stop
	{"\"", "″"}, // ″  Double Prime
	{">", "›"},  // ›  Single Right-Pointing Angle Quotation
	{"<", "‹"},  // ‹  Single Left-Pointing Angle Quotation
	{"?", "？"},  // ？ Fullwidth Question Mark
}

var multiSpaceRe = regexp.MustCompile(`\s+`)

// replaceInvalidPathCharacters replaces characters that are illegal in
// filesystem paths with Unicode lookalikes, then NFC-normalises the result.
// It also converts "..." to the Unicode ellipsis character and strips leading/
// trailing dots (matching .NET Kaizoku behaviour).
func replaceInvalidPathCharacters(s string) string {
	if s == "" {
		// Defensive path: the assembled filename from GenerateCBZFilename always begins
		// with "[provider]" so s is never empty; this guard protects against future callers.
		return s
	}
	for _, pair := range invalidPathCharMap {
		s = strings.ReplaceAll(s, pair[0], pair[1])
	}
	s = strings.ReplaceAll(s, "...", "…") // … Ellipsis
	s = strings.Trim(s, ".")
	return strings.TrimSpace(norm.NFC.String(s))
}

// collapseSpaces replaces every run of whitespace with a single space and
// trims leading/trailing whitespace.
func collapseSpaces(s string) string {
	return strings.TrimSpace(multiSpaceRe.ReplaceAllString(s, " "))
}

// isTitleChapter reports whether name is a redundant "Chapter N" label that
// duplicates information already present in the chapter number.
func isTitleChapter(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(lower, "chapter ") ||
		strings.HasPrefix(lower, "ch. ") ||
		strings.HasPrefix(lower, "ch ")
}
