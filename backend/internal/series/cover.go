package series

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
)

// coverVersionLen is how much of the source-URL digest goes into the ?v= cache
// buster. 12 hex chars (48 bits) is far beyond any collision risk across a
// personal library, and keeps the URL readable.
const coverVersionLen = 12

// coverVersion derives the cover URL's cache-busting version from the metadata
// provider's cover_url — the SAME string that keys the on-disk cache, so the
// version changes exactly when the served image can change (a metadata-source
// switch, or the source publishing a new thumbnail) and never otherwise.
//
// It is a pure string hash: building a series DTO costs NO disk I/O, which is
// the point — the list endpoint must not stat 52 cover files to render a grid.
func coverVersion(sourceURL string) string {
	sum := sha256.Sum256([]byte(sourceURL))
	return hex.EncodeToString(sum[:])[:coverVersionLen]
}

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
// and NO source is pinged while the recorded source_url still equals the metadata
// provider's current cover_url. Only a mismatch — the owner switched metadata
// source, or the source changed its thumbnail (ingest refreshes cover_url) —
// triggers exactly one re-fetch, which overwrites the local copy. This is what
// stops a 52-series library grid from firing 52 source-ward fetches on every
// single render.
//
// The lookup is a THREE-STEP ladder, cheapest first:
//
//  1. DB fast-index (Series.cover_file + cover_source_url) — one os.ReadFile and
//     nothing else. This is the hot path a library grid hits.
//  2. Sidecar (disk.ReadCover) — the pre-index fallback. An existing library has
//     covers + sidecar cover blocks on disk but EMPTY columns, and treating that
//     as "not cached" would re-fetch every cover from the sources: exactly the
//     hammering this cache exists to prevent. A sidecar hit therefore serves the
//     bytes AND backfills the two columns, so the series self-heals onto step 1.
//  3. Suwayomi — the only step that touches a source, reached only when there is
//     no valid local cover at all.
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

	// Step 1 — the fast index. The row is already in memory, so a warm serve is a
	// single file read: no sidecar, no JSON parse (a 300-chapter sidecar over NFS
	// is a real cost to pay per cover), no hashing.
	if row.CoverFile != "" && row.CoverSourceURL == meta.CoverURL {
		if data, ext, readErr := disk.ReadCoverFile(s.storage, categoryName, row.Title, row.CoverFile); readErr == nil {
			return data, ext, nil
		}
		// The file vanished under us — fall through and re-fetch it once.
	}

	// Step 2 — the sidecar, the durable seed the index is derived from.
	if data, ext, prov, readErr := disk.ReadCover(s.storage, categoryName, row.Title); readErr == nil {
		if prov.SourceURL == meta.CoverURL {
			s.indexCover(ctx, row.ID, prov.File, prov.SourceURL)
			return data, ext, nil
		}
	}

	// Step 3 — the source.
	return s.fetchAndCacheCover(ctx, row, meta, categoryName)
}

// indexCover records which cover file the series currently holds and which
// source URL it came from, so subsequent serves take the fast path.
//
// It is BEST-EFFORT by design: the bytes are already in hand, and the sidecar
// still holds the same facts, so a failed index write costs a slow serve next
// time — never a failed page and never a source fetch.
func (s *Service) indexCover(ctx context.Context, id uuid.UUID, file, sourceURL string) {
	err := s.client.Series.UpdateOneID(id).
		SetCoverFile(file).
		SetCoverSourceURL(sourceURL).
		Exec(ctx)
	if err != nil {
		slog.Warn("cover index write failed", "series_id", id, "error", err)
	}
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

	filename, saveErr := disk.SaveCover(disk.CoverRequest{
		Storage:   s.storage,
		Category:  categoryName,
		Title:     row.Title,
		Data:      data,
		Ext:       ext,
		SourceURL: meta.CoverURL,
		Provider:  meta.Provider,
	})
	switch {
	case saveErr == nil:
		// The file landed: point the fast index at it so every later serve skips
		// both the sidecar and the source.
		s.indexCover(ctx, row.ID, filename, meta.CoverURL)
	case errors.Is(saveErr, disk.ErrNoSeriesDir):
		// A cache that cannot persist must not break the page: serve the bytes, and
		// let the next view try to cache again. A series with no folder on disk
		// (nothing downloaded yet) is the EXPECTED case, not a fault — SaveCover
		// never creates the folder, so those series simply stay live-proxied.
		slog.Debug("cover not cached: series has no folder on disk",
			"series_id", row.ID, "title", row.Title)
	default:
		slog.Warn("cover cache write failed",
			"series_id", row.ID, "title", row.Title, "error", saveErr)
	}

	return data, ext, nil
}
