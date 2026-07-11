package series

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
)

// ErrCoverFetchFailed is returned by CoverBytes when the cover is not cached
// locally AND Suwayomi cannot supply it. The HTTP handler maps it to a 502: the
// upstream is a separate service, so its failure is a gateway error, never a
// false 200.
var ErrCoverFetchFailed = errors.New("cover fetch failed")

// CoverFetcher is the narrow Suwayomi port CoverBytes needs — the one method of
// suwayomi.Client that fetches image bytes. Depending on the method rather than
// the whole client keeps the series domain free of the Suwayomi package and
// makes the "zero calls when cached" proof trivial to assert on.
type CoverFetcher interface {
	PageBytes(ctx context.Context, url string) ([]byte, string, error)
}

// WithCoverFetcher attaches the Suwayomi cover fetcher and returns the service,
// so production can wire it fluently onto either constructor. It is optional:
// a service without one still serves a cached cover, and reports
// ErrCoverFetchFailed rather than panicking if a cold cover needs fetching.
func (s *Service) WithCoverFetcher(f CoverFetcher) *Service {
	s.sw = f
	return s
}

// CoverBytes returns the series cover image and its bare extension, serving the
// LOCAL copy whenever possible.
//
// The cache rule, in full: the cover stored in the series folder is authoritative
// and NO source is pinged while the sidecar's recorded source_url still equals the
// metadata provider's current cover_url. Only a mismatch — the owner switched
// metadata source, or the source changed its thumbnail (ingest refreshes
// cover_url) — triggers exactly one re-fetch, which overwrites the local copy.
// This is what stops a 52-series library grid from firing 52 source-ward fetches
// on every single render.
//
// Failure modes: unknown series → ErrSeriesNotFound; no metadata provider or no
// stored cover_url → ErrNoCover (both 404). A fetch failure on a COLD cover →
// ErrCoverFetchFailed (502) — nothing partial is ever written. A DISK failure is
// deliberately NOT fatal: the fetched bytes are still returned (and the failure
// logged), because a cache that cannot persist must not break the page.
func (s *Service) CoverBytes(ctx context.Context, id uuid.UUID) (data []byte, ext string, err error) {
	row, err := s.client.Series.Query().
		Where(entseries.IDEQ(id)).
		WithProviders().
		WithCategory().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, "", ErrSeriesNotFound
		}
		return nil, "", fmt.Errorf("series.CoverBytes: load series %s: %w", id, err)
	}

	// Resolved the same way CoverURL does (the shared MetadataProvider resolver) —
	// but from THIS row, because the cache path also needs the category + title to
	// find the series folder. One query, not two.
	meta := MetadataProvider(row)
	if meta == nil || meta.CoverURL == "" {
		return nil, "", ErrNoCover
	}

	categoryName := category.NameOf(row)

	if data, ext, prov, readErr := disk.ReadCover(s.storage, categoryName, row.Title); readErr == nil {
		if prov.SourceURL == meta.CoverURL {
			return data, ext, nil
		}
	}

	return s.fetchAndCacheCover(ctx, row, meta, categoryName)
}

// fetchAndCacheCover fetches the metadata provider's cover from Suwayomi once,
// stores it in the series folder (best-effort), and returns the bytes. Called
// only when the local copy is absent or stale — see CoverBytes for the rule.
func (s *Service) fetchAndCacheCover(
	ctx context.Context,
	row *ent.Series,
	meta *ent.SeriesProvider,
	categoryName string,
) (data []byte, ext string, err error) {
	if s.sw == nil {
		return nil, "", fmt.Errorf("%w: no cover fetcher configured", ErrCoverFetchFailed)
	}

	data, rawExt, err := s.sw.PageBytes(ctx, meta.CoverURL)
	if err != nil {
		return nil, "", fmt.Errorf("%w: series %s: %w", ErrCoverFetchFailed, row.ID, err)
	}

	// Normalise ONCE, the same way the store does, so the cold response and the
	// warm (read-back-from-disk) response report the identical extension — an
	// upstream "JPEG" must not serve as octet-stream cold and image/jpeg warm.
	ext = disk.NormalizeCoverExt(rawExt)

	if _, saveErr := disk.SaveCover(disk.CoverRequest{
		Storage:   s.storage,
		Category:  categoryName,
		Title:     row.Title,
		Data:      data,
		Ext:       ext,
		SourceURL: meta.CoverURL,
		Provider:  meta.Provider,
	}); saveErr != nil {
		// A cache that cannot persist must not break the page: serve the bytes, and
		// let the next view try to cache again. A series with no folder on disk
		// (nothing downloaded yet) is the EXPECTED case, not a fault — SaveCover
		// never creates the folder, so those series simply stay live-proxied.
		if errors.Is(saveErr, disk.ErrNoSeriesDir) {
			slog.Debug("cover not cached: series has no folder on disk",
				"series_id", row.ID, "title", row.Title)
		} else {
			slog.Warn("cover cache write failed",
				"series_id", row.ID, "title", row.Title, "error", saveErr)
		}
	}

	return data, ext, nil
}
