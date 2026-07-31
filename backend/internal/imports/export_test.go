// Package imports — test-only exports.
//
// Compiled only during `go test`. Exposes the unexported cache seams so
// black-box (package imports_test) cache tests can inject a real chapter cache
// and a clock-controlled search cache without a settings key or a real clock.
package imports

import (
	"context"
	"time"

	"github.com/technobecet/tsundoku/internal/ingest"
)

// SetChapterCacheForTest wires cache into s so its discovery paths
// (SourceBreakdown / InspectChapters) route through it (Task C2).
func SetChapterCacheForTest(s *Service, cache *ingest.ChapterCache) {
	s.chapterCache = cache
}

// SetSearchCacheForTest wires a search-result cache into s with the given PER-Get
// TTL provider and an injectable clock, so a black-box test can drive Task C1
// hit/expiry AND TTL hot reload deterministically by advancing now / mutating the
// provider instead of sleeping. A provider returning 0 disables the cache.
func SetSearchCacheForTest(s *Service, ttl func(context.Context) time.Duration, now func() time.Time) {
	sc := newSearchCache(ttl)
	sc.now = now
	s.searchCache = sc
}

// Test-only accessors for the unexported coverage store (GAP-140). Keeping the
// store unexported preserves the service's surface while letting the black-box
// tests drive it directly.

// ExportLoadCoverage exposes the unexported loadCoverage.
func ExportLoadCoverage(s *Service, ctx context.Context, sourceID, mangaURL string) (CoverageSnapshot, bool, error) {
	return s.loadCoverage(ctx, sourceID, mangaURL)
}

// ExportSaveCoverage exposes the unexported saveCoverage.
func ExportSaveCoverage(s *Service, ctx context.Context, sourceID, mangaURL string, dto SourceBreakdownDTO) error {
	return s.saveCoverage(ctx, sourceID, mangaURL, dto)
}

// ExportFailCoverage exposes the unexported failCoverage.
func ExportFailCoverage(s *Service, ctx context.Context, sourceID, mangaURL string, cause error) error {
	return s.failCoverage(ctx, sourceID, mangaURL, cause)
}

// ExportMarkCoveragePending exposes the unexported markCoveragePending.
func ExportMarkCoveragePending(s *Service, ctx context.Context, sourceID, mangaURL string) error {
	return s.markCoveragePending(ctx, sourceID, mangaURL)
}
