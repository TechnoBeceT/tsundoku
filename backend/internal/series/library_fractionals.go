package series

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
)

// SeriesFractionalsDTO is one row of the library-wide Fractionals page: a series
// that has at least one DOWNLOADED fractional chapter on disk, with the two
// counts the owner acts on plus the whole-series ignore-policy state.
//
// FractionalCount is EVERY downloaded fractional (ignore-agnostic) — the reason
// the series is listed at all, and it deliberately includes OLD series whose
// sources were never flagged (the owner's real pain). RemovableCount is how many
// of those are removable RIGHT NOW under the strict resurrection-safe rule (see
// FractionalCleanupPreview: downloaded + filed + fractional + ≥1 carrier + EVERY
// carrier ignores fractionals). Showing both side by side tells the owner "how
// much junk is here" AND "how much I can clean before I set any policy".
//
// ProvidersTotal / ProvidersIgnoring feed the whole-series ignore toggle, and
// AllProvidersIgnoring is its resolved on/off state — computed server-side so the
// FE renders the answer rather than re-deriving it (§11). The toggle reads ON only
// when EVERY source already ignores fractionals; flipping it sets them all
// (SetIgnoreFractionalForSeries).
type SeriesFractionalsDTO struct {
	SeriesID             string `json:"seriesId"`
	Title                string `json:"title"`
	DisplayName          string `json:"displayName"`
	Category             string `json:"category"`
	CoverURL             string `json:"coverUrl"`
	FractionalCount      int    `json:"fractionalCount"`
	RemovableCount       int    `json:"removableCount"`
	ProvidersTotal       int    `json:"providersTotal"`
	ProvidersIgnoring    int    `json:"providersIgnoring"`
	AllProvidersIgnoring bool   `json:"allProvidersIgnoring"`
}

// LibraryFractionalsDTO is the library-wide Fractionals page envelope: every
// series carrying ≥1 downloaded fractional chapter, sorted most-actionable first.
// Series is always non-nil so the JSON renders [] rather than null (mirrors
// LibraryHealthDTO).
type LibraryFractionalsDTO struct {
	Series []SeriesFractionalsDTO `json:"series"`
}

// LibraryFractionals lists every series that has at least one DOWNLOADED
// fractional chapter, so the owner can retroactively set the ignore-fractional
// policy and clean the leftover files from ONE place instead of hunting
// series-by-series (his daily pain). A series appears REGARDLESS of its ignore
// state — the point is OLD un-flagged series, which must be visible so the owner
// can set the policy THEN clean.
//
// NO N+1: one bounded load (every series' chapters + providers + their feeds +
// category, all eager-loaded) then a pure in-memory computation per series — the
// removable set, the counts, the display name and the cover all resolve from the
// already-loaded rows, never a per-series query. Pinned by
// TestLibraryFractionalsQueryCountIsSeriesCountIndependent.
func (s *Service) LibraryFractionals(ctx context.Context) (LibraryFractionalsDTO, error) {
	rows, err := s.loadAllSeriesForCleanup(ctx)
	if err != nil {
		return LibraryFractionalsDTO{}, err
	}

	out := LibraryFractionalsDTO{Series: []SeriesFractionalsDTO{}}
	for _, row := range rows {
		fractionalCount := downloadedFractionalCount(row)
		if fractionalCount == 0 {
			continue // list criterion: only series with a downloaded fractional
		}
		total, ignoring := providerIgnoreCounts(row.Edges.Providers)
		name, coverURL := SeriesDisplay(row, MetadataProvider(row))
		out.Series = append(out.Series, SeriesFractionalsDTO{
			SeriesID:             row.ID.String(),
			Title:                row.Title,
			DisplayName:          name,
			Category:             category.NameOf(row),
			CoverURL:             coverURL,
			FractionalCount:      fractionalCount,
			RemovableCount:       len(removableFractionals(row)),
			ProvidersTotal:       total,
			ProvidersIgnoring:    ignoring,
			AllProvidersIgnoring: total > 0 && ignoring == total,
		})
	}
	sortLibraryFractionals(out.Series)
	return out, nil
}

// loadAllSeriesForCleanup loads every series with the edges the library-wide
// cleanup rollups (fractional + sourceless) need, in one bounded query set (Ent
// batch-loads each edge in a single query regardless of series count): chapters
// (the fractional/sourceless tallies + the page-count median), providers WITH
// their availability feeds (the carriers behind the resurrection guard / the
// zero-carrier sourceless rule), and category (the display + folder name).
// Mirrors loadSeriesWithHealthData's no-N+1 shape.
func (s *Service) loadAllSeriesForCleanup(ctx context.Context) ([]*ent.Series, error) {
	rows, err := s.client.Series.Query().
		Order(entseries.ByTitle()).
		WithChapters().
		WithProviders(func(pq *ent.SeriesProviderQuery) {
			pq.WithProviderChapters()
		}).
		WithCategory().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("series.loadAllSeriesForCleanup: %w", err)
	}
	return rows, nil
}

// providerIgnoreCounts tallies a series' providers and how many currently ignore
// fractionals — the inputs to the whole-series ignore toggle's resolved state.
func providerIgnoreCounts(providers []*ent.SeriesProvider) (total, ignoring int) {
	for _, p := range providers {
		total++
		if p.IgnoreFractional {
			ignoring++
		}
	}
	return total, ignoring
}

// sortLibraryFractionals orders the page most-actionable first: the most
// removable-right-now on top (removableCount desc), then the biggest fractional
// backlog (fractionalCount desc), then title A→Z as a stable, deterministic
// tiebreak.
func sortLibraryFractionals(rows []SeriesFractionalsDTO) {
	slices.SortStableFunc(rows, func(a, b SeriesFractionalsDTO) int {
		if d := cmp.Compare(b.RemovableCount, a.RemovableCount); d != 0 {
			return d
		}
		if d := cmp.Compare(b.FractionalCount, a.FractionalCount); d != 0 {
			return d
		}
		return cmp.Compare(a.Title, b.Title)
	})
}
