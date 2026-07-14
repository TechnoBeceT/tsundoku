// Package metadatasvc is the Phase-1 native metadata engine's orchestration
// layer: it drives the pure internal/metadata Registry (search / name-match /
// merge) against real Series rows, persisting results durably (Series jsonb
// columns + the tsundoku.json sidecar Metadata block — see
// spec/metadata-engine-phase1 §3) and caching a chosen cover through the
// existing Local Cover Cache (internal/disk.SaveCover).
//
// 🔴 NEVER-LINK-A-SOURCE INVARIANT: every method here writes metadata + cover
// ONLY. None of them create, modify, or delete a SeriesProvider or Chapter row
// — auto-identify (or a manual owner pick) must never imply a download
// source, so the library's "Needs source" signal stays accurate regardless of
// how rich a series' metadata is.
//
// # Why this is its own package, not internal/metadata
//
// The generated internal/ent package already imports internal/metadata for
// the Series jsonb field types (AltTitle/Author/Link/SourceRef — see
// internal/ent/schema/series.go). A service in internal/metadata that itself
// imported internal/ent would therefore cycle: ent → metadata → ent. This
// package sits one layer above both, mirroring how internal/metadata/
// providers sits above the metadata↔provider cycle for the same structural
// reason (see that package's doc comment): internal/metadatasvc imports
// internal/metadata (pure contracts + Registry) + internal/ent (generated
// client) + internal/disk (sidecar + cover cache) + internal/category (the
// on-disk category-folder-name resolver), one-directional, no cycle.
package metadatasvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/metadata"
)

// ErrSeriesNotFound is returned when the given series id matches no row. The
// HTTP handler layer (a later slice) maps it to 404.
var ErrSeriesNotFound = errors.New("series not found")

// ErrProviderNotFound is returned by Identify when providerKey names no
// registered metadata provider. The HTTP handler layer maps it to 400 (the
// caller supplied a bad provider key, not a missing resource).
var ErrProviderNotFound = errors.New("metadata provider not found")

// ErrSourceCoverFetcherNotConfigured is returned by SetCover when kind=="source"
// but no SourceCoverFetcher was attached via WithSourceCoverFetcher — e.g. a
// test-built Service via plain NewService, or a wiring gap. A clear error
// beats a nil-pointer panic on the very first source-cover pick.
var ErrSourceCoverFetcherNotConfigured = errors.New("metadatasvc: no source cover fetcher configured")

// defaultHTTPTimeout bounds a single cover-image fetch performed by
// saveCoverFromURL. A cover is one image, not a paginated API call, so a
// generous-but-bounded client-wide timeout (rather than a per-request
// context deadline the caller must remember to set) is the same shape
// series.Service's Suwayomi-backed cover fetch relies on its caller's ctx
// for — here there is no caller-owned ctx budget to lean on for an
// arbitrary third-party image host, so the client itself carries the cap.
const defaultHTTPTimeout = 30 * time.Second

// Service is the metadata-engine orchestration service: an Ent client (the
// durable index), a Registry of assembled providers (internal/metadata/
// providers.NewRegistry in production; a fake metadata.NewRegistry(...) in
// tests), the library storage root (for the sidecar + cover cache paths),
// and the HTTP client used to fetch chosen cover bytes.
type Service struct {
	client   *ent.Client
	registry *metadata.Registry
	storage  string
	http     *http.Client
	// sourceCoverFetcher is the optional port SetCover uses to fetch a
	// library SOURCE's own cover bytes (kind=="source"). Attached via
	// WithSourceCoverFetcher; nil until then (see SourceCoverFetcher's doc
	// comment for why the concrete adapter is NOT built in this package).
	sourceCoverFetcher SourceCoverFetcher
}

// Option configures a Service at construction time. The only production use
// today is the test seam below (WithHTTPClient) — production callers pass no
// options.
type Option func(*Service)

// WithHTTPClient overrides the Service's HTTP client. This is the TEST SEAM
// for the SSRF-safe production default (newSSRFSafeHTTPClient, httpclient.go):
// that client deliberately refuses to dial loopback/private/link-local
// addresses, which would block every test that reaches a local
// httptest.Server. Tests pass a plain *http.Client via this option; a
// production caller (main.go, the metadata HTTP handler wiring) calls
// NewService with no options and gets the guarded client automatically.
func WithHTTPClient(c *http.Client) Option {
	return func(s *Service) { s.http = c }
}

// SourceCoverFetcher is the narrow port SetCover uses to fetch a library
// SOURCE's own cover bytes (SetCover kind=="source") — resolving the
// SeriesProvider's stored cover_url (with the same series↔provider ownership
// check the per-provider cover proxy route performs) and fetching those bytes
// from Suwayomi. It is deliberately this narrow, mirroring
// series.Service.CoverFetcher, for two reasons: it keeps metadatasvc's own
// import surface exactly the one its package doc declares (metadata + ent +
// disk + category — resolving a SeriesProvider's cover_url needs
// internal/series, and fetching it needs internal/suwayomi, neither of which
// this package imports), and it makes SetCover's source branch trivial to
// fake in tests. The concrete adapter lives at the composition root
// (cmd/tsundoku), wired via WithSourceCoverFetcher.
type SourceCoverFetcher interface {
	SourceCoverBytes(ctx context.Context, seriesID, providerID uuid.UUID) (data []byte, ext string, err error)
}

// WithSourceCoverFetcher attaches the port SetCover uses to fetch a library
// source's own cover bytes, mirroring series.Service.WithCoverFetcher's
// fluent shape so production wires it the same way. It is optional: a
// Service built without one still serves metadata-provider cover picks; a
// "source"-kind SetCover call returns ErrSourceCoverFetcherNotConfigured
// instead of a nil-pointer panic.
func (s *Service) WithSourceCoverFetcher(f SourceCoverFetcher) *Service {
	s.sourceCoverFetcher = f
	return s
}

// NewService builds the metadata-engine orchestration service. http defaults
// to newSSRFSafeHTTPClient() — a bounded-timeout client whose dialer refuses
// non-public destinations (see httpclient.go) — because AutoIdentify follows
// PROVIDER-supplied cover URLs automatically: that path is reachable from
// untrusted external data, not just an owner-typed URL, so the default must
// be safe without any caller opting in. Pass WithHTTPClient to override (see
// its doc comment for the test seam this exists for).
func NewService(client *ent.Client, registry *metadata.Registry, storage string, opts ...Option) *Service {
	s := &Service{
		client:   client,
		registry: registry,
		storage:  storage,
		http:     newSSRFSafeHTTPClient(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Search delegates to the Registry's fan-out search across providerKeys (all
// registered providers when empty) — the live candidate gallery behind
// GET /api/metadata/search. Nothing is persisted; see Identify for the
// picking step.
func (s *Service) Search(ctx context.Context, q string, providerKeys []string) ([]metadata.SearchResult, error) {
	return s.registry.Search(ctx, q, providerKeys)
}

// AutoIdentify runs the background, best-effort identify pass triggered
// after a series is imported/adopted (spec/metadata-engine-phase1 §4): it
// matches the series' OWN title (+ any alt-titles it already carries) against
// every registered provider, and when at least one confidently matches,
// merges their metadata (primary-anchored per QCAT-228, primary = the
// registry-priority-ordered first match) and persists it.
//
// "No confident match anywhere" is an EXPECTED outcome, not a failure — the
// series' metadata is simply left untouched and AutoIdentify returns nil, so
// a caller firing this from a detached goroutine (the C5 import/adopt hook)
// never needs special-case handling for "nothing found" vs "identified".
func (s *Service) AutoIdentify(ctx context.Context, seriesID uuid.UUID) error {
	row, err := s.client.Series.Query().Where(entseries.IDEQ(seriesID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrSeriesNotFound
		}
		return fmt.Errorf("metadatasvc: load series %s: %w", seriesID, err)
	}

	mq := metadata.MatchQuery{Title: row.Title, AltTitles: altTitleNames(row.AltTitles)}
	result, err := s.registry.Identify(ctx, mq, nil)
	if err != nil {
		return fmt.Errorf("metadatasvc: AutoIdentify series %s: %w", seriesID, err)
	}
	if len(result.Order) == 0 {
		return nil // no provider confidently matched — best-effort, not an error.
	}

	primaryKey := result.Order[0]
	src := metadata.SourceRef{Kind: "metadata", Ref: primaryKey}
	var coverURL string
	// result.Matches is index-aligned with result.Order (both are appended in
	// the same loop iteration inside Registry.Identify), so Matches[0] is
	// always the primary's own match — the source of its cover candidate.
	if len(result.Matches) > 0 {
		primaryMatch := result.Matches[0]
		src.RemoteID = primaryMatch.RemoteID
		src.RemoteURL = primaryMatch.RemoteURL
		coverURL = primaryMatch.CoverURL
	}

	return s.persist(ctx, seriesID, result.Merged, src, coverURL)
}

// Identify performs the owner's manual "anchor-then-aggregate" pick
// (spec/metadata-engine-phase1 §5): the CHOSEN (providerKey, remoteID) pair
// always becomes the primary (metadata_source, Order[0]) — never displaced by
// an auto-match, unlike AutoIdentify's registry-priority primary. Every OTHER
// registered provider is then auto-matched by the primary's own title/
// alt-titles and folded in as scalar gap-fill + collection union (QCAT-228).
//
// ErrProviderNotFound when providerKey names no registered provider;
// ErrSeriesNotFound when seriesID matches no row. Any other error is a
// genuine upstream/persistence failure (the picked provider's own
// GetSeriesMetadata call failing is NOT best-effort — the owner explicitly
// asked for that record).
func (s *Service) Identify(ctx context.Context, seriesID uuid.UUID, providerKey, remoteID string) error {
	provider, ok := s.registry.Provider(providerKey)
	if !ok {
		return ErrProviderNotFound
	}

	exists, err := s.client.Series.Query().Where(entseries.IDEQ(seriesID)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("metadatasvc: check series %s: %w", seriesID, err)
	}
	if !exists {
		return ErrSeriesNotFound
	}

	primary, err := provider.GetSeriesMetadata(ctx, remoteID)
	if err != nil {
		return fmt.Errorf("metadatasvc: fetch %s metadata for remote id %q: %w", providerKey, remoteID, err)
	}

	metas := map[string]metadata.SeriesMetadata{providerKey: primary}
	order := []string{providerKey}

	otherKeys := otherProviderKeys(s.registry, providerKey)
	if len(otherKeys) > 0 {
		mq := metadata.MatchQuery{Title: primary.Title, AltTitles: altTitleNames(primary.AltTitles)}
		others, othersErr := s.registry.Identify(ctx, mq, otherKeys)
		if othersErr != nil {
			return fmt.Errorf("metadatasvc: identify other providers for series %s: %w", seriesID, othersErr)
		}
		// others.Matches only carries the search-result-shaped ProviderMatch
		// (no full SeriesMetadata), so each confirmed other provider's own
		// metadata is re-fetched here to build a genuine per-provider metas
		// map for Merge — a provider that fails on this second call is
		// logged and skipped, never fatal to the identify as a whole.
		for _, pm := range others.Matches {
			op, found := s.registry.Provider(pm.ProviderKey)
			if !found {
				continue // defensive: Registry.Identify only returns keys it holds.
			}
			full, ferr := op.GetSeriesMetadata(ctx, pm.RemoteID)
			if ferr != nil {
				slog.WarnContext(ctx, "metadatasvc: re-fetch metadata for merge failed",
					"provider", pm.ProviderKey, "remote_id", pm.RemoteID, "err", ferr)
				continue
			}
			metas[pm.ProviderKey] = full
			order = append(order, pm.ProviderKey)
		}
	}

	merged := metadata.Merge(metadata.MergeInput{Metas: metas, Order: order})

	src := metadata.SourceRef{
		Kind:      "metadata",
		Ref:       providerKey,
		RemoteID:  remoteID,
		RemoteURL: resolvePrimaryURL(ctx, provider, primary.Title, remoteID),
	}

	return s.persist(ctx, seriesID, merged, src, primary.CoverURL)
}

// CoverCandidates aggregates cover options from every metadata provider AND
// the series' own library SOURCES for the series' own title — the gallery
// behind GET /api/series/:id/metadata/covers. It reuses Registry.Search's
// existing fan-out (deterministic, registry-order, per-provider-failure-
// skipped) rather than looping over providers itself, so provider-fan-out
// logic has exactly one home; the source half is appended from the already
// eager-loaded SeriesProvider rows (sourceCoverCandidates) — zero extra
// queries, zero source calls.
//
// DEFERRED (noted, not built here — see the C1 task report): MangaDex's
// multi-cover gallery endpoint isn't reachable through the Provider
// interface's Search/GetSeriesCover methods, so only ONE cover per MangaDex
// search hit surfaces today.
func (s *Service) CoverCandidates(ctx context.Context, seriesID uuid.UUID) ([]metadata.CoverCandidate, error) {
	row, err := s.client.Series.Query().
		Where(entseries.IDEQ(seriesID)).
		WithProviders().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrSeriesNotFound
		}
		return nil, fmt.Errorf("metadatasvc: load series %s: %w", seriesID, err)
	}

	hits, err := s.registry.Search(ctx, row.Title, nil)
	if err != nil {
		return nil, fmt.Errorf("metadatasvc: search covers for series %s: %w", seriesID, err)
	}

	candidates := make([]metadata.CoverCandidate, 0, len(hits)+len(row.Edges.Providers))
	for _, h := range hits {
		if h.CoverURL == "" {
			continue
		}
		candidates = append(candidates, metadata.CoverCandidate{
			SourceKind: "metadata",
			SourceRef:  h.Provider,
			CoverURL:   h.CoverURL,
			Label:      h.Provider,
		})
	}
	candidates = append(candidates, sourceCoverCandidates(seriesID, row.Edges.Providers)...)
	return candidates, nil
}

// sourceCoverCandidates appends one CoverCandidate per SeriesProvider that
// carries its own cover_url — the series' own library sources (Comix,
// WeebCentral, a disk-origin scanlator group, ...) — so the owner can pick a
// source's poster alongside the metadata-provider gallery.
//
// CoverURL is the per-provider cover PROXY path
// (/api/series/{id}/providers/{providerId}/cover — the same browser-loadable,
// same-origin, authed route the metadata-source picker's thumbnails already
// use, see handler/series.ProviderCover) — NEVER the raw
// SeriesProvider.cover_url, which is Suwayomi's own server-relative thumbnail
// path: the browser cannot load it directly (no auth forwarded, and Suwayomi
// is often only reachable from the backend).
func sourceCoverCandidates(seriesID uuid.UUID, providers []*ent.SeriesProvider) []metadata.CoverCandidate {
	out := make([]metadata.CoverCandidate, 0, len(providers))
	for _, p := range providers {
		if p.CoverURL == "" {
			continue
		}
		out = append(out, metadata.CoverCandidate{
			SourceKind: "source",
			SourceRef:  p.ID.String(),
			CoverURL:   fmt.Sprintf("/api/series/%s/providers/%s/cover", seriesID, p.ID),
			Label:      sourceProviderLabel(p),
		})
	}
	return out
}

// sourceProviderLabel is the display label for a source cover candidate — the
// source's human-readable provider_name, falling back to the raw provider
// identity key when no name was captured. This duplicates
// series.ProviderLabel's two-line fallback rather than importing
// internal/series into a cover-candidate helper that otherwise needs none of
// it (the same "trivial duplication beats a cross-package dependency" call
// coverVersion makes below, for the identical reason: this package's declared
// import surface is metadata + ent + disk + category).
func sourceProviderLabel(p *ent.SeriesProvider) string {
	if p.ProviderName != "" {
		return p.ProviderName
	}
	return p.Provider
}

// SetCover records the owner's explicit cover pick — cover_source = {kind,
// ref, coverURL} — independent of metadata_source (QCAT-228: cover selection
// is never coupled to the rich-metadata merge). Unlike persist's best-effort
// cover step, a fetch/cache failure here IS returned: the whole point of the
// call is to change the cover, so the owner must see it fail.
//
// The bytes come from one of two places depending on kind: a "metadata"
// candidate is fetched by an ordinary HTTP GET of coverURL (a public
// provider/CDN URL — saveCoverFromURL); a "source" candidate's bytes live on
// the Suwayomi server, not at coverURL (which is the browser-loadable PROXY
// path CoverCandidates handed back — see sourceCoverCandidates), so it is
// resolved + fetched through the SourceCoverFetcher port instead
// (saveCoverFromSource).
func (s *Service) SetCover(ctx context.Context, seriesID uuid.UUID, kind, ref, coverURL string) error {
	row, err := s.client.Series.Query().
		Where(entseries.IDEQ(seriesID)).
		WithCategory().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrSeriesNotFound
		}
		return fmt.Errorf("metadatasvc: load series %s: %w", seriesID, err)
	}
	categoryName := category.NameOf(row)

	if kind == "source" {
		return s.saveCoverFromSource(ctx, row, categoryName, ref, coverURL)
	}
	return s.saveCoverFromURL(ctx, row, categoryName, kind, ref, coverURL)
}

// persist writes the merged rich metadata (Description/Status/Genres/Tags/
// AltTitles/Authors/Links/Year) plus the resolved metadata_source onto the
// Series row, mirrors the fresh row into the durable tsundoku.json sidecar
// Metadata block (disk is the rebuild seed — disk.Reconcile's
// restoreMetadataIndex reads it back after a DB loss), and — when coverURL is
// non-empty — best-effort fetches and caches the chosen cover. It is the ONE
// write path shared by AutoIdentify and Identify; it NEVER touches
// SeriesProvider or Chapter (see the package doc's never-link-a-source
// invariant).
func (s *Service) persist(
	ctx context.Context,
	seriesID uuid.UUID,
	merged metadata.SeriesMetadata,
	src metadata.SourceRef,
	coverURL string,
) error {
	original, err := s.client.Series.Query().
		Where(entseries.IDEQ(seriesID)).
		WithCategory().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrSeriesNotFound
		}
		return fmt.Errorf("metadatasvc: load series %s: %w", seriesID, err)
	}
	categoryName := category.NameOf(original)

	updated, err := s.client.Series.UpdateOne(original).
		SetDescription(merged.Description).
		SetStatus(merged.Status).
		SetGenres(merged.Genres).
		SetTags(merged.Tags).
		SetAltTitles(merged.AltTitles).
		SetAuthors(merged.Authors).
		SetLinks(merged.Links).
		SetYear(merged.Year).
		SetMetadataSource(&src).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("metadatasvc: persist metadata for series %s: %w", seriesID, err)
	}

	s.writeSidecarBestEffort(updated, categoryName)

	if coverURL == "" {
		return nil
	}
	if coverErr := s.saveCoverFromURL(ctx, updated, categoryName, src.Kind, src.Ref, coverURL); coverErr != nil {
		// Cover is best-effort inside an identify pass: the rich metadata above
		// has already landed, and a transient image fetch/disk failure must not
		// undo it (mirrors series.Service.fetchAndCacheCover's own non-fatal
		// disk-write handling for the SAME Local Cover Cache).
		slog.WarnContext(ctx, "metadatasvc: cover fetch/cache failed during identify",
			"series_id", seriesID, "provider", src.Ref, "err", coverErr)
	}
	return nil
}

// saveCoverFromURL fetches coverURL's bytes over plain HTTP (a metadata
// provider's public CDN URL) and hands them to finalizeCover. Returns any
// fetch/persist error — SetCover propagates it to the owner; persist's
// identify callers treat it as best-effort and only log.
func (s *Service) saveCoverFromURL(
	ctx context.Context,
	row *ent.Series,
	categoryName, kind, ref, coverURL string,
) error {
	data, ext, err := s.fetchCoverBytes(ctx, coverURL)
	if err != nil {
		return err
	}
	return s.finalizeCover(ctx, row, categoryName, kind, ref, coverURL, data, ext)
}

// saveCoverFromSource resolves ref (a SeriesProvider UUID string) through the
// attached SourceCoverFetcher and hands the fetched bytes to finalizeCover.
// coverURL here is the browser-loadable proxy path CoverCandidates handed
// back (see sourceCoverCandidates) — recorded as provenance, never fetched
// directly (the real bytes live on the Suwayomi server, resolved by the
// SourceCoverFetcher via the SeriesProvider's own cover_url).
//
// Returns ErrSourceCoverFetcherNotConfigured when no port is attached, an
// invalid-UUID error when ref does not parse, or whatever the port itself
// reports (e.g. series.ErrProviderNotInSeries when ref does not belong to
// row's series).
func (s *Service) saveCoverFromSource(
	ctx context.Context,
	row *ent.Series,
	categoryName, ref, coverURL string,
) error {
	if s.sourceCoverFetcher == nil {
		return fmt.Errorf("metadatasvc: set source cover for series %s: %w", row.ID, ErrSourceCoverFetcherNotConfigured)
	}
	providerID, err := uuid.Parse(ref)
	if err != nil {
		return fmt.Errorf("metadatasvc: invalid source provider id %q: %w", ref, err)
	}

	data, ext, err := s.sourceCoverFetcher.SourceCoverBytes(ctx, row.ID, providerID)
	if err != nil {
		return fmt.Errorf("metadatasvc: fetch source cover for series %s provider %s: %w", row.ID, providerID, err)
	}
	return s.finalizeCover(ctx, row, categoryName, "source", ref, coverURL, data, ext)
}

// finalizeCover is the shared tail of both cover-set paths: caches data via
// disk.SaveCover (the same Local Cover Cache the M10 cover proxy +
// series.Service.CoverBytes use), re-indexes Series.cover_file/
// cover_source_url/cover_version + cover_source in one update, and mirrors
// the fresh row into the sidecar.
func (s *Service) finalizeCover(
	ctx context.Context,
	row *ent.Series,
	categoryName, kind, ref, coverURL string,
	data []byte,
	ext string,
) error {
	filename, err := disk.SaveCover(disk.CoverRequest{
		Storage:   s.storage,
		Category:  categoryName,
		Title:     row.Title,
		Data:      data,
		Ext:       ext,
		SourceURL: coverURL,
		Provider:  ref,
	})
	if err != nil {
		return fmt.Errorf("metadatasvc: cache cover for series %s: %w", row.ID, err)
	}

	coverSrc := metadata.SourceRef{Kind: kind, Ref: ref, RemoteURL: coverURL}
	updated, err := s.client.Series.UpdateOne(row).
		SetCoverFile(filename).
		SetCoverSourceURL(coverURL).
		SetCoverVersion(coverVersion(data)).
		SetCoverSource(&coverSrc).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("metadatasvc: index cover for series %s: %w", row.ID, err)
	}

	s.writeSidecarBestEffort(updated, categoryName)
	return nil
}

// maxCoverBytes bounds a single cover-image fetch. A cover is one image, not
// a paginated payload, so the prior unbounded io.ReadAll let any provider-
// or owner-supplied URL stream an unbounded amount of data into memory —
// this caps it at a generous 20 MiB.
const maxCoverBytes = 20 << 20

// fetchCoverBytes performs the http GET for a chosen cover URL and returns
// the raw bytes plus a best-guess bare extension derived from the response's
// (declared or sniffed) content type (disk.SaveCover/NormalizeCoverExt
// degrade an unrecognised or empty extension to a safe default, so a miss
// here is never fatal — only cosmetic). The read is bounded (maxCoverBytes)
// and the response is rejected outright if it is clearly not an image.
func (s *Service) fetchCoverBytes(ctx context.Context, coverURL string) (data []byte, ext string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, coverURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("metadatasvc: build cover request for %q: %w", coverURL, err)
	}

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("metadatasvc: fetch cover %q: %w", coverURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("metadatasvc: fetch cover %q: unexpected status %d", coverURL, resp.StatusCode)
	}
	if resp.ContentLength > maxCoverBytes {
		return nil, "", fmt.Errorf("metadatasvc: fetch cover %q: declared size %d exceeds %d byte limit",
			coverURL, resp.ContentLength, maxCoverBytes)
	}

	// Read one byte past the limit so an oversized body is DETECTED (len(body)
	// > maxCoverBytes) rather than silently truncated into a corrupt image.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("metadatasvc: read cover body %q: %w", coverURL, err)
	}
	if len(body) > maxCoverBytes {
		return nil, "", fmt.Errorf("metadatasvc: fetch cover %q: body exceeds %d byte limit", coverURL, maxCoverBytes)
	}

	mediaType, err := classifyCoverContentType(resp.Header.Get("Content-Type"), body)
	if err != nil {
		return nil, "", fmt.Errorf("metadatasvc: fetch cover %q: %w", coverURL, err)
	}

	return body, extFromContentType(mediaType), nil
}

// classifyCoverContentType resolves the effective image media type for a
// fetched cover response: a declared "image/*" Content-Type is trusted
// outright; otherwise the body is sniffed (http.DetectContentType) so a
// provider serving a real image behind a generic or absent Content-Type (this
// varies across providers) is not wrongly rejected. A response that is
// clearly NOT an image — a declared non-image Content-Type whose body also
// doesn't sniff as one (an HTML error page, a JSON error body), or no
// Content-Type at all over non-image bytes — is rejected.
func classifyCoverContentType(headerContentType string, body []byte) (string, error) {
	if base := mediaTypeBase(headerContentType); strings.HasPrefix(base, "image/") {
		return base, nil
	}

	sniffed := mediaTypeBase(http.DetectContentType(body[:min(len(body), 512)]))
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed, nil
	}

	if headerContentType != "" {
		return "", fmt.Errorf("response is not an image (content-type %q)", headerContentType)
	}
	return "", errors.New("response does not look like an image")
}

// mediaTypeBase strips any ";charset=..." parameters and lowercases, so
// "image/png; charset=binary" and "Image/PNG" both compare as "image/png".
func mediaTypeBase(contentType string) string {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return strings.ToLower(strings.TrimSpace(contentType))
}

// writeSidecarBestEffort mirrors row's current rich-metadata + cover-source
// columns into the series' tsundoku.json sidecar Metadata block. It builds
// the block from row (the FRESHLY UPDATED entity, already reflecting
// whichever fields this call changed plus every field it didn't), so the
// disk snapshot is always the full current truth — never a partial patch a
// later disk.Reconcile could misread.
//
// A series with no folder on disk yet (nothing downloaded) is the EXPECTED
// case, not a fault: disk.WriteMetadata never creates the series directory
// (mirroring disk.SaveCover's ErrNoSeriesDir — see that sentinel), so this is
// silently skipped there; the DB columns above still persist, and the
// sidecar catches up the first time the series gets a folder. Any OTHER disk
// error is logged, not fatal — a cache that cannot persist must not break
// the identify.
func (s *Service) writeSidecarBestEffort(row *ent.Series, categoryName string) {
	seriesDir := disk.SeriesDir(s.storage, categoryName, row.Title)
	block := disk.SeriesMetadataSidecar{
		Description:    row.Description,
		Status:         row.Status,
		Genres:         row.Genres,
		Tags:           row.Tags,
		AltTitles:      row.AltTitles,
		Authors:        row.Authors,
		Links:          row.Links,
		Year:           row.Year,
		MetadataSource: row.MetadataSource,
		CoverSource:    row.CoverSource,
	}

	if err := disk.WriteMetadata(seriesDir, block); err != nil {
		if errors.Is(err, disk.ErrNoSeriesDir) {
			slog.Debug("metadatasvc: sidecar not written: series has no folder on disk",
				"series_id", row.ID, "title", row.Title)
			return
		}
		slog.Warn("metadatasvc: sidecar write failed", "series_id", row.ID, "error", err)
	}
}

// otherProviderKeys returns every registered provider's Key() except
// exclude, in registration (priority) order.
//
// GOTCHA: Registry.selectProviders treats an EMPTY keys slice as "every
// provider" (see internal/metadata/registry.go), so callers must NEVER pass
// this straight through when it comes back empty (the only-one-provider-
// registered edge case) — that would silently re-include the excluded
// provider. Identify guards this explicitly before calling registry.Identify.
func otherProviderKeys(r *metadata.Registry, exclude string) []string {
	all := r.Providers()
	keys := make([]string, 0, len(all))
	for _, p := range all {
		if p.Key() == exclude {
			continue
		}
		keys = append(keys, p.Key())
	}
	return keys
}

// resolvePrimaryURL best-effort resolves the picked provider's own canonical
// URL for remoteID. metadata.SeriesMetadata carries no URL field of its own
// (only external reference Links, e.g. an official site) — only
// Provider.Search's SearchResult does — so this re-runs Search on the
// already-fetched primary title and returns the hit whose RemoteID matches.
// A Search failure or no matching RemoteID degrades to "": the URL is
// provenance detail for owner-facing display, never correctness-critical to
// the identify itself.
func resolvePrimaryURL(ctx context.Context, provider metadata.Provider, title, remoteID string) string {
	hits, err := provider.Search(ctx, title, 0)
	if err != nil {
		return ""
	}
	for _, h := range hits {
		if h.RemoteID == remoteID {
			return h.URL
		}
	}
	return ""
}

// altTitleNames flattens a []metadata.AltTitle into its bare display names —
// the shape MatchQuery.AltTitles wants (match.go's NameSimilarity compares
// plain title strings, not the {Name,Type,Lang} structure).
func altTitleNames(alts []metadata.AltTitle) []string {
	if len(alts) == 0 {
		return nil
	}
	out := make([]string, 0, len(alts))
	for _, a := range alts {
		out = append(out, a.Name)
	}
	return out
}

// extFromContentType maps a cover response's Content-Type header to a bare
// image extension disk.NormalizeCoverExt will accept. An unrecognised or
// empty header returns "", which NormalizeCoverExt degrades to its own safe
// default — this never needs to be exhaustive.
func extFromContentType(contentType string) string {
	switch {
	case strings.Contains(contentType, "png"):
		return "png"
	case strings.Contains(contentType, "webp"):
		return "webp"
	case strings.Contains(contentType, "gif"):
		return "gif"
	case strings.Contains(contentType, "jpeg"), strings.Contains(contentType, "jpg"):
		return "jpg"
	default:
		return ""
	}
}

// coverVersionLen mirrors internal/series/cover.go's own constant: 12 hex
// chars (48 bits) of a sha256 digest is far beyond any collision risk across
// a personal library, and keeps the URL readable.
const coverVersionLen = 12

// coverVersion is a short hash of cover image BYTES — the content version a
// served cover URL carries (see internal/series/cover.go's coverVersion,
// which this deliberately duplicates in miniature rather than import: this
// package must not import internal/series, see the package doc's cycle
// rationale, and the primitive itself is a 3-line pure hash-and-truncate with
// no other coupling worth sharing across an import boundary).
func coverVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:coverVersionLen]
}
