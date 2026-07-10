// Package sourcefilter holds the shared ?sources CSV parsing reused by every
// handler package that narrows a cross-source search to a named subset of
// sources (imports' GET /api/search, library's GET /api/library/imports/match).
// Both previously needed the identical split-trim-drop-empties logic; this
// package is the single source of truth (§2 DRY) so a change to the parsing
// rule only has to happen once.
package sourcefilter

import "strings"

// Parse parses the optional ?sources CSV query parameter. An empty or absent
// parameter returns nil (meaning "all sources"). Non-empty tokens that appear
// after splitting are trimmed and returned; blank tokens are dropped. Unknown
// source IDs are NOT validated here — the imports service's resolveSources
// silently drops them (documented choice: validating against the live client
// list would require a round-trip before the search itself).
func Parse(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
