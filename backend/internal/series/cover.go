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

// coverVersionLen is how much of the image digest goes into the ?v= cache
// buster. 12 hex chars (48 bits) is far beyond any collision risk across a
// personal library, and keeps the URL readable.
const coverVersionLen = 12

// coverVersion is the CONTENT version of a cover: a short hash of the image
// BYTES. It is what the served URL carries (…/cover?v=<version>) and therefore
// what licenses the endpoint's `immutable` response.
//
// GOTCHA — it must NEVER be derived from the provider's cover_url. That URL is
// Suwayomi's id-derived thumbnail path (/api/v1/manga/{id}/thumbnail), so it is
// stable across a source republishing different art: a URL-derived version would
// leave the browser pinned to a stale image for a YEAR (immutable is a one-way
// door, and the only lever that can show a new image is a NEW URL). Hashing the
// bytes makes "the URL changes whenever the content does" literally true.
//
// It is only ever computed where the bytes are already in hand (a fetch, a save,
// a disk read) — the version reaches a DTO from the Series.cover_version COLUMN,
// so building a DTO still costs ZERO disk I/O.
func coverVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:coverVersionLen]
}

// Cover is one served series cover: the image bytes, its bare extension, and the
// content version of those exact bytes (empty when nothing is cached on disk —
// see coverVersion).
type Cover struct {
	// Data is the raw image, exactly as the source served it.
	Data []byte

	// Ext is the bare, normalised image extension ("jpg", "webp", …).
	Ext string

	// Version is the short content hash of Data, mirroring Series.cover_version.
	// Empty means these bytes are NOT cached on disk (a live-proxied series, or a
	// cache write that failed), so the response must not be marked immutable.
	Version string
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

// CoverBytes returns the series cover image, its bare extension, and the content
// version of those bytes — serving the LOCAL copy whenever possible.
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
//     bytes AND backfills the index, so the series self-heals onto step 1.
//  3. Suwayomi — the only step that touches a source, reached only when there is
//     no valid local cover at all.
//
// Every step that has the bytes in hand re-derives their content version and
// re-indexes when it has drifted, so cover_version can never lie about what is on
// disk — the endpoint stakes an `immutable` response on it (see coverVersion).
//
// Failure modes: unknown series → ErrSeriesNotFound; no metadata provider or no
// stored cover_url → ErrNoCover (both 404). A fetch failure on a COLD cover →
// ErrCoverFetchFailed (502) — nothing partial is ever written. A DISK failure is
// deliberately NOT fatal: the fetched bytes are still returned (and the failure
// logged), because a cache that cannot persist must not break the page.
func (s *Service) CoverBytes(ctx context.Context, id uuid.UUID) (Cover, error) {
	row, err := s.client.Series.Query().
		Where(entseries.IDEQ(id)).
		WithProviders().
		WithCategory().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Cover{}, ErrSeriesNotFound
		}
		return Cover{}, fmt.Errorf("series.CoverBytes: load series %s: %w", id, err)
	}

	// Resolved the same way CoverURL does (the shared MetadataProvider resolver) —
	// but from THIS row, because the cache path also needs the category + title to
	// find the series folder. One query, not two.
	meta := MetadataProvider(row)
	if meta == nil || meta.CoverURL == "" {
		return Cover{}, ErrNoCover
	}

	categoryName := category.NameOf(row)

	if cover, ok := s.localCover(ctx, row, meta, categoryName); ok {
		return cover, nil
	}

	// Step 3 — the source: no usable local copy at all.
	return s.fetchAndCacheCover(ctx, row, meta, categoryName)
}

// localCover resolves steps 1 and 2 of the ladder — the DB fast-index, then the
// sidecar — and reports whether a usable local cover was found. It NEVER touches
// a source; a false return is the only thing that lets CoverBytes fetch.
//
// Both steps re-derive the content version from the bytes they actually read and
// re-index when it has drifted, so cover_version can never lie about what is on
// disk (the cover endpoint stakes an `immutable` response on it, and a stale
// version would pin the old image in the browser with no lever to fix it).
func (s *Service) localCover(
	ctx context.Context,
	row *ent.Series,
	meta *ent.SeriesProvider,
	categoryName string,
) (Cover, bool) {
	// Step 1 — the fast index. The row is already in memory, so a warm serve is a
	// single file read: no sidecar, no JSON parse (a 300-chapter sidecar over NFS
	// is a real cost to pay per cover).
	if row.CoverFile != "" && row.CoverSourceURL == meta.CoverURL {
		if data, ext, err := disk.ReadCoverFile(s.storage, categoryName, row.Title, row.CoverFile); err == nil {
			return s.indexedCover(ctx, row.ID, row.CoverFile, meta.CoverURL, row.CoverVersion, data, ext), true
		}
		// The file vanished under us — fall through and re-fetch it once.
	}

	// Step 2 — the sidecar, the durable seed the index is derived from. An existing
	// library (covers on disk, empty columns) lands here and self-heals onto step 1.
	if data, ext, prov, err := disk.ReadCover(s.storage, categoryName, row.Title); err == nil {
		if prov.SourceURL == meta.CoverURL {
			return s.indexedCover(ctx, row.ID, prov.File, prov.SourceURL, row.CoverVersion, data, ext), true
		}
	}

	return Cover{}, false
}

// indexedCover versions the bytes just read from disk and writes the index back
// when it has drifted from what the DB holds (an empty version after a reconcile,
// or a cover file edited out of band). Returns the served Cover.
func (s *Service) indexedCover(
	ctx context.Context,
	id uuid.UUID,
	file, sourceURL, storedVersion string,
	data []byte,
	ext string,
) Cover {
	version := coverVersion(data)
	if version != storedVersion {
		s.indexCover(ctx, id, file, sourceURL, version)
	}
	return Cover{Data: data, Ext: ext, Version: version}
}

// CoverVersion returns the content version of the series' cached cover, or ""
// when nothing is cached for it.
//
// It is the CHEAP half of the cover endpoint: one column read, no disk, no
// fetch. A conditional request (If-None-Match) is answered from this alone, so a
// 304 never pays for reading the image — which is the entire point of a 304.
// Unknown series → ErrSeriesNotFound.
func (s *Service) CoverVersion(ctx context.Context, id uuid.UUID) (string, error) {
	version, err := s.client.Series.Query().
		Where(entseries.IDEQ(id)).
		Select(entseries.FieldCoverVersion).
		String(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", ErrSeriesNotFound
		}
		return "", fmt.Errorf("series.CoverVersion: load series %s: %w", id, err)
	}
	return version, nil
}

// indexCover records which cover file the series currently holds, which source
// URL it came from, and the content version of its bytes, so subsequent serves
// take the fast path and the served URL is correctly versioned.
//
// It is BEST-EFFORT by design: the bytes are already in hand, and the sidecar
// still holds the same facts, so a failed index write costs a slow serve next
// time — never a failed page and never a source fetch.
func (s *Service) indexCover(ctx context.Context, id uuid.UUID, file, sourceURL, version string) {
	err := s.client.Series.UpdateOneID(id).
		SetCoverFile(file).
		SetCoverSourceURL(sourceURL).
		SetCoverVersion(version).
		Exec(ctx)
	if err != nil {
		slog.Warn("cover index write failed", "series_id", id, "error", err)
	}
}

// fetchAndCacheCover fetches the metadata provider's cover from Suwayomi once,
// stores it in the series folder (best-effort), and returns it. Called only when
// the local copy is absent or stale — see CoverBytes for the rule.
//
// The returned Version is non-empty ONLY when the bytes actually landed on disk:
// an uncached (live-proxied) cover must not carry a version, because the endpoint
// would then mark it immutable while nothing durable backs that promise.
func (s *Service) fetchAndCacheCover(
	ctx context.Context,
	row *ent.Series,
	meta *ent.SeriesProvider,
	categoryName string,
) (Cover, error) {
	if s.sw == nil {
		return Cover{}, fmt.Errorf("%w: no cover fetcher configured", ErrCoverFetchFailed)
	}

	data, rawExt, err := s.sw.PageBytes(ctx, meta.CoverURL)
	if err != nil {
		return Cover{}, fmt.Errorf("%w: series %s: %w", ErrCoverFetchFailed, row.ID, err)
	}

	// Normalise ONCE, the same way the store does, so the cold response and the
	// warm (read-back-from-disk) response report the identical extension — an
	// upstream "JPEG" must not serve as octet-stream cold and image/jpeg warm.
	cover := Cover{Data: data, Ext: disk.NormalizeCoverExt(rawExt)}

	filename, saveErr := disk.SaveCover(disk.CoverRequest{
		Storage:   s.storage,
		Category:  categoryName,
		Title:     row.Title,
		Data:      cover.Data,
		Ext:       cover.Ext,
		SourceURL: meta.CoverURL,
		Provider:  meta.Provider,
	})
	switch {
	case saveErr == nil:
		// The file landed: version it and point the fast index at it, so every later
		// serve skips both the sidecar and the source — and the URL the DTO emits
		// changes, because these are different bytes.
		cover.Version = coverVersion(cover.Data)
		s.indexCover(ctx, row.ID, filename, meta.CoverURL, cover.Version)
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

	return cover, nil
}
