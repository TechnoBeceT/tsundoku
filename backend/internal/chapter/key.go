// Package chapter provides identity utilities for manga chapters.
//
// The canonical chapter identity string (chapter_key) is defined here. It is
// the sole source of truth for how raw provider data is normalised into the
// stable key stored in the database. Everything downstream — ingest, dedup,
// disk layout, reconciliation — relies on this normalisation being deterministic
// and collision-free.
package chapter

import (
	"regexp"
	"strconv"
	"strings"
)

// reStripNonSlug matches any character that is not a lowercase ASCII letter,
// digit, dot, or hyphen. It is applied after whitespace has been collapsed to
// hyphens so that the resulting slug contains only [a-z0-9.-].
var reStripNonSlug = regexp.MustCompile(`[^a-z0-9.-]`)

// FormatChapterNumber converts a chapter number to its canonical decimal string.
//
// It uses the shortest exact representation: trailing zeros after the decimal
// point are dropped (12.0 → "12", 12.5 → "12.5", 12.05 → "12.05", 0 → "0").
// This canonical form is used as the chapter_key for all numbered chapters and
// must never be altered without a migration.
func FormatChapterNumber(n float64) string {
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// NormalizeChapterKey derives the stable chapter_key that uniquely identifies a
// chapter within its series.
//
// Rules:
//   - Non-nil number: the key is FormatChapterNumber(*number). The name is
//     ignored entirely.
//   - Nil number: the key is "name:" + slug(name). If name is also empty the
//     result is "name:" — callers should avoid this combination, but it is
//     defined so the function is total.
//
// The returned string is safe for use as a database key and as a path segment.
func NormalizeChapterKey(number *float64, name string) string {
	if number != nil {
		return FormatChapterNumber(*number)
	}

	return "name:" + slug(name)
}

// slug converts a raw string into a URL/key-safe identifier.
//
// Steps applied in order:
//  1. Lowercase the input.
//  2. Trim leading and trailing whitespace.
//  3. Collapse every internal run of whitespace characters to a single hyphen.
//  4. Strip any character outside [a-z0-9.-].
func slug(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimSpace(s)
	// Replace each run of whitespace with a single hyphen.
	s = strings.Join(strings.Fields(s), "-")
	// Remove characters that are not in the allowed set.
	s = reStripNonSlug.ReplaceAllString(s, "")

	return s
}
