// Package providerid owns the ONE rule that reads a SeriesProvider.provider
// column value as the engine-host's numeric source id — and therefore the ONE
// rule that tells a LIVE provider apart from a DISK-ORIGIN one.
//
// The two SeriesProvider create paths deliberately disagree about what they
// store in `provider`: internal/ingest writes the engine-host's NUMERIC source
// id ("99"), while disk reconcile writes the source's display NAME ("Asura
// Scans"). So "this row is backed by a live engine source" is simply "provider
// parses as an integer", and the parsed value is the id every engine-facing
// caller needs.
//
// WHY IT LIVES IN pkg/ AND NOT IN internal/series: the rule's long-standing home
// was series.LinkedProviderSourceID, which still is the entry point series,
// library and refresh use. But internal/series IMPORTS internal/chapter, so
// chapter can never import series back — and internal/chapter's candidate
// ranking needs exactly this parse to drop an owner-paused source (QCAT-513).
// A second copy of the parse in chapter is precisely the fork series/linked.go's
// doc comment warns against (refresh once carried one and it had already
// diverged by not trimming whitespace), so the implementation moved down here
// into a stateless kernel with no domain imports, and series/linked.go now
// delegates to it. One implementation, every entry point unchanged.
package providerid

import (
	"strconv"
	"strings"
)

// SourceID parses a RAW SeriesProvider.provider value as the engine-host's
// numeric source id.
//
// It returns (id, true) for a LIVE provider (the column holds the numeric source
// id, e.g. "99"), and (0, false) for a DISK-ORIGIN provider (the column holds a
// display NAME, e.g. "Asura Scans") or any other unparseable value. Surrounding
// whitespace is trimmed first: a value persisted as " 8 " is the same live source
// as "8", and treating it as disk-origin was a real divergence between two copies
// of this rule.
//
// A false second return is NOT an error condition — a disk-origin provider simply
// has no engine source behind it, and every caller has a defined answer for that
// (refresh skips it, the cover chain falls back, the candidate ranking leaves it
// alone).
func SourceID(provider string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(provider), 10, 64)
	return id, err == nil
}
