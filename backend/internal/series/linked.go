package series

import (
	"strconv"
	"strings"

	"github.com/technobecet/tsundoku/internal/ent"
)

// IsLinkedProvider reports whether p is a real, linked LIVE source (attached
// via internal/ingest, directly or through a Match/AddProvider merge) as
// opposed to a disk-origin provider (created by library import/reconcile/the
// Kaizoku migration, never a real source).
//
// The P2 Suwayomi-removal migration retired SuwayomiID as the discriminator:
// internal/ingest creates live providers WITHOUT setting it (chapters/mangas
// are now URL-addressed, so there is no numeric manga id to store), so
// `SuwayomiID != 0` now reads false for every freshly-adopted live source.
// The new identity model tells linked/disk-origin apart from
// SeriesProvider.Provider itself: a live provider stores the engine-host's
// NUMERIC source id string (e.g. "99"); a disk-origin provider stores a
// display NAME (e.g. "Asura Scans"). So "linked" is simply "Provider parses
// as an integer" — the same rule internal/refresh applies to build its refresh
// groups, and it calls LinkedProviderSourceID directly for it (refresh once
// carried a private copy that had already diverged by not trimming whitespace).
//
// Both `series` and `library` need this predicate (`library` already imports
// `series`, never the reverse), so it lives here rather than in `library`.
func IsLinkedProvider(p *ent.SeriesProvider) bool {
	_, ok := LinkedProviderSourceID(p.Provider)
	return ok
}

// ProviderSourceID parses p.Provider as the engine-host's numeric source id —
// the SAME parse IsLinkedProvider performs (they share this one implementation,
// §2 DRY), but returning the parsed value for callers that need the id itself,
// not just the linked/disk-origin bool. The cover-fetch chain (series/cover.go,
// series.Service.ProviderCoverURL) uses this to resolve which engine source to
// call sourceengine.Client.Image against: a disk-origin provider (Provider is a
// display NAME) has no engine source at all, so ok is false and the caller
// falls back to its existing cold-fetch-failed handling.
func ProviderSourceID(p *ent.SeriesProvider) (int64, bool) {
	return LinkedProviderSourceID(p.Provider)
}

// LinkedProviderSourceID is the string-level primitive both predicates above
// delegate to: it parses a RAW SeriesProvider.provider value as the engine-host's
// numeric source id, reporting ok=false for a disk-origin display NAME. It exists
// for callers that only ever hold the column value and not the whole row — a
// PROJECTED query that selects (series_id, provider) alone to find the
// disk-origin providers in a library (see library.driftedSeriesIDs, GAP-120)
// would otherwise have to hydrate every entity just to ask this one question,
// and internal/refresh needs the parsed id itself to key a fetch group.
// One implementation, four entry points (§2 DRY) — IsLinkedProvider,
// ProviderSourceID, library.driftedSeriesIDs and refresh.buildRefreshGroups —
// so the linked/disk-origin rule can never fork.
func LinkedProviderSourceID(provider string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(provider), 10, 64)
	return id, err == nil
}
