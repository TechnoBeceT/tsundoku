package series

import (
	"cmp"
	"context"
	"slices"

	"github.com/technobecet/tsundoku/internal/category"
)

// SeriesSourcelessDTO is one row of the library-wide Sourceless page: a series that
// has at least one DOWNLOADED chapter no remaining source carries, with the count the
// owner acts on and the display name/cover for the card.
type SeriesSourcelessDTO struct {
	SeriesID        string `json:"seriesId"`
	Title           string `json:"title"`
	DisplayName     string `json:"displayName"`
	Category        string `json:"category"`
	CoverURL        string `json:"coverUrl"`
	SourcelessCount int    `json:"sourcelessCount"`
}

// LibrarySourcelessDTO is the library-wide Sourceless page envelope, sorted
// most-actionable first. Series is always non-nil so the JSON renders [] not null.
type LibrarySourcelessDTO struct {
	Series []SeriesSourcelessDTO `json:"series"`
}

// LibrarySourceless lists every series with ≥1 sourceless downloaded chapter, so the
// owner can clean stranded CBZs from ONE place. NO N+1: one bounded load (every
// series' chapters + providers + feeds + category eager-loaded) then a pure in-memory
// removableSourceless count per series.
func (s *Service) LibrarySourceless(ctx context.Context) (LibrarySourcelessDTO, error) {
	rows, err := s.loadAllSeriesForCleanup(ctx)
	if err != nil {
		return LibrarySourcelessDTO{}, err
	}

	out := LibrarySourcelessDTO{Series: []SeriesSourcelessDTO{}}
	for _, row := range rows {
		count := len(removableSourceless(row))
		if count == 0 {
			continue
		}
		name, coverURL := SeriesDisplay(row, MetadataProvider(row))
		out.Series = append(out.Series, SeriesSourcelessDTO{
			SeriesID:        row.ID.String(),
			Title:           row.Title,
			DisplayName:     name,
			Category:        category.NameOf(row),
			CoverURL:        coverURL,
			SourcelessCount: count,
		})
	}
	sortLibrarySourceless(out.Series)
	return out, nil
}

// sortLibrarySourceless orders the page most-actionable first: highest sourceless
// count on top, then title A→Z as a stable, deterministic tiebreak.
func sortLibrarySourceless(rows []SeriesSourcelessDTO) {
	slices.SortStableFunc(rows, func(a, b SeriesSourcelessDTO) int {
		if d := cmp.Compare(b.SourcelessCount, a.SourcelessCount); d != 0 {
			return d
		}
		return cmp.Compare(a.Title, b.Title)
	})
}
