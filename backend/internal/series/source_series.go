package series

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/predicate"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
)

// SourceSeriesDTO is one row of the "what depends on this source" read model
// (GET /api/sources/{sourceId}/series, QCAT-513): a series that carries the
// source, plus what happens to it if the source is paused. It backs the owner's
// pre-pause impact view — how many series lose their only provider vs. keep an
// alternative.
type SourceSeriesDTO struct {
	// SeriesID is the series' UUID as a string.
	SeriesID string `json:"seriesId"`
	// Title is the series' display title (metadata-source title when pinned,
	// else the canonical row title) — resolved through SeriesDisplay so it
	// matches the library list/detail label.
	Title string `json:"title"`
	// AlternativeCount is how many of the series' providers are NOT this source
	// — the sources that would still feed it after this one is paused.
	AlternativeCount int `json:"alternativeCount"`
	// GoesDark is true exactly when AlternativeCount == 0: pausing this source
	// leaves the series with no provider at all (no source can fetch new
	// chapters). Already-downloaded chapters stay on disk regardless.
	GoesDark bool `json:"goesDark"`
	// TopAlternative is the display label of the highest-importance provider
	// that is NOT this source — the source that would take over. Empty when the
	// series goes dark.
	TopAlternative string `json:"topAlternative"`
}

// SeriesForSource returns every series that carries the given source, each with
// its alternative-provider count, whether it "goes dark" on pause, and the
// highest-importance take-over provider. It productises the impact query behind
// the per-source pause (QCAT-513): before pausing a source the owner sees which
// series depend on it and which have no fallback.
//
// 🔴 SOURCE MATCHING crosses BOTH SeriesProvider create paths (the live-vs-disk
// identity drift). A live-ingested row stores the engine-host's NUMERIC source id
// in `provider`; a disk-reconciled row stores the display NAME there. So a source
// is matched when either holds: series.ProviderSourceID(row) == sourceID (the
// live rows, via the shared internal/pkg/providerid kernel) OR row.Provider ==
// sourceName (the disk rows). providerBelongsToSource is the single in-memory
// definition of that rule, and sourceProviderPredicate is its DB-predicate twin —
// they must stay in step. This mirrors the same both-paths matcher the bulk
// re-download uses (downloads.redownloadSourcePredicates) so the two can never
// disagree about what "this source" means.
//
// sourceName must be the source's resolved display name (the handler resolves it
// from the engine registry); it is required to catch disk-origin rows, whose
// `provider` IS that name. An empty name yields an empty result rather than
// matching every disk row — the handler already answers an unresolved/uninstalled
// source id with an empty list, and this is the belt-and-suspenders guard.
//
// NO-N+1 by construction: ONE query selects the series carrying the source and
// eager-loads all their providers (a single sub-query, series-count independent —
// mirroring RankedLiveCandidatesForMany's bucket-in-memory shape), then every
// per-series count is a pure in-memory fold over the loaded provider edges. The
// statement count does not grow with the number of matched series.
func (s *Service) SeriesForSource(ctx context.Context, sourceID int64, sourceName string) ([]SourceSeriesDTO, error) {
	name := strings.TrimSpace(sourceName)
	if name == "" {
		return []SourceSeriesDTO{}, nil
	}
	idStr := strconv.FormatInt(sourceID, 10)

	rows, err := s.client.Series.Query().
		Where(entseries.HasProvidersWith(sourceProviderPredicate(idStr, name))).
		WithProviders().
		Order(entseries.ByTitle()).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("series.SeriesForSource: load series carrying source %d: %w", sourceID, err)
	}

	out := make([]SourceSeriesDTO, 0, len(rows))
	for _, row := range rows {
		alternatives := alternativeProviders(row.Edges.Providers, sourceID, name)
		top := ""
		if best := HighestImportanceProvider(alternatives); best != nil {
			top = ProviderLabel(best)
		}
		title, _ := SeriesDisplay(row, MetadataProvider(row))
		out = append(out, SourceSeriesDTO{
			SeriesID:         row.ID.String(),
			Title:            title,
			AlternativeCount: len(alternatives),
			GoesDark:         len(alternatives) == 0,
			TopAlternative:   top,
		})
	}
	return out, nil
}

// alternativeProviders returns the providers of a series that are NOT the given
// source — the fallbacks that survive a pause of it. A series that carries the
// source as BOTH a live row and a disk row (the drift case: one numeric-provider
// row + one name-provider row, same physical source) correctly excludes both, so
// the same source is never counted as its own alternative.
func alternativeProviders(providers []*ent.SeriesProvider, sourceID int64, name string) []*ent.SeriesProvider {
	alts := make([]*ent.SeriesProvider, 0, len(providers))
	for _, p := range providers {
		if !providerBelongsToSource(p, sourceID, name) {
			alts = append(alts, p)
		}
	}
	return alts
}

// providerBelongsToSource is the in-memory half of the both-create-paths source
// match: a LIVE row belongs to the source when its parsed numeric provider equals
// sourceID; a DISK-origin row (whose provider does not parse as a number) belongs
// when its provider equals the display name. It shares the parse with the rest of
// the codebase through series.ProviderSourceID (→ internal/pkg/providerid), so the
// linked/disk-origin rule can never fork.
func providerBelongsToSource(p *ent.SeriesProvider, sourceID int64, name string) bool {
	if id, ok := ProviderSourceID(p); ok {
		return id == sourceID
	}
	return p.Provider == name
}

// sourceProviderPredicate is the DB half of the same match: a SeriesProvider row
// whose `provider` is the numeric source id (live) OR the display name (disk).
// It is used inside HasProvidersWith to select only the series that carry the
// source, so the whole library is never loaded. Kept in step with
// providerBelongsToSource by construction — both express "provider == idStr OR
// provider == name".
//
// KNOWN-MINOR (shared with downloads.redownloadSourcePredicates): the name match
// is exact, so a disk-origin provider persisted with surrounding whitespace would
// not match. The same divergence already affects the re-download filter and the
// breaker join, so it is a data defect rather than one this query introduces.
func sourceProviderPredicate(idStr, name string) predicate.SeriesProvider {
	return entseriesprovider.Or(
		entseriesprovider.ProviderEQ(idStr),
		entseriesprovider.ProviderEQ(name),
	)
}
