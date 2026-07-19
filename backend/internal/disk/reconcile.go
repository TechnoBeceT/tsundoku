package disk

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entcategory "github.com/technobecet/tsundoku/internal/ent/category"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	enttrackbinding "github.com/technobecet/tsundoku/internal/ent/trackbinding"
)

// ReconcileResult reports what Reconcile changed in the DB on a single run.
//
// Idempotency guarantee: a fully idempotent second run (unchanged library,
// existing DB rows) produces ChaptersAdopted == 0 and no row-count growth.
// The Upserted counters (SeriesUpserted, ProvidersUpserted, ChaptersUpserted)
// reflect rows PROCESSED (created or updated) on this run, not only newly
// created; they will be non-zero even on a second run over an unchanged library.
type ReconcileResult struct {
	// SeriesUpserted is the number of Series rows created or updated this run.
	// Non-zero even on a second run — counts every series that was processed.
	SeriesUpserted int

	// ProvidersUpserted is the number of SeriesProvider rows created or updated
	// this run. Non-zero even on a second run.
	ProvidersUpserted int

	// ChaptersUpserted is the number of Chapter rows set to downloaded with
	// updated provenance this run. Non-zero even on a second run — counts every
	// chapter that was processed (created or updated), not only newly created.
	ChaptersUpserted int

	// ChaptersAdopted is the number of Chapter rows that did not exist in the DB
	// and were created (adopted) from disk. Zero on a fully idempotent re-run.
	ChaptersAdopted int

	// MissingFiles is the number of sidecar entries whose CBZ file is absent on
	// disk. Reported only; no state transition is forced.
	MissingFiles int
}

// reSlugStrip matches any character outside the allowed slug set [a-z0-9-].
var reSlugStrip = regexp.MustCompile(`[^a-z0-9-]`)

// Slugify derives a deterministic, URL-safe identifier from a series title.
//
// Steps:
//  1. Lowercase the string.
//  2. Trim leading/trailing whitespace.
//  3. Collapse each run of whitespace to a single hyphen.
//  4. Strip characters outside [a-z0-9-].
//
// Exported so the M2 ingest service can produce the same slug for a given
// title, guaranteeing that ingest and disk reconciliation agree on identity.
func Slugify(title string) string {
	s := strings.ToLower(title)
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), "-")
	s = reSlugStrip.ReplaceAllString(s, "")
	return s
}

// Reconcile scans the storage root and idempotently upserts Series,
// SeriesProvider, and Chapter rows into the database.
//
// It is the lossless-rebuild proof for Milestone 1: after a total DB loss,
// running Reconcile against an intact library directory restores all chapter
// rows with their original keys, state, provider, importance, filename, and
// page_count.
//
// Missing files (sidecar entries whose CBZ is absent) are counted in
// ReconcileResult.MissingFiles. No illegal state transition is forced.
func Reconcile(ctx context.Context, client *ent.Client, storage string) (ReconcileResult, error) {
	var result ReconcileResult

	facts, err := ScanLibrary(storage)
	if err != nil {
		// Defensive path: reachable only on OS-level I/O failure (permission denied /
		// fd exhausted) when reading the library directory tree.
		return result, fmt.Errorf("disk.Reconcile: scan: %w", err)
	}

	for _, sf := range facts {
		if err := reconcileSeries(ctx, client, sf, &result); err != nil {
			// Defensive path: reachable only when the DB connection is lost mid-run or
			// a concurrent writer causes an unretryable constraint violation.
			return result, err
		}
	}

	return result, nil
}

// ReconcileOne imports a single already-scanned series into the database,
// reusing the same per-series upsert path as full Reconcile. It find-or-creates
// the Series (+ its Category), the per-provider SeriesProvider rows, and the
// Chapter rows (state=downloaded, satisfied_by the disk provider). No disk I/O,
// no deletion, no state regression.
func ReconcileOne(ctx context.Context, client *ent.Client, sf SeriesFacts) (ReconcileResult, error) {
	var result ReconcileResult
	if err := reconcileSeries(ctx, client, sf, &result); err != nil {
		return ReconcileResult{}, err
	}
	return result, nil
}

// reconcileSeries upserts one series and all its providers and chapters.
func reconcileSeries(ctx context.Context, client *ent.Client, sf SeriesFacts, result *ReconcileResult) error {
	series, err := upsertSeries(ctx, client, sf.Title, sf.Category)
	if err != nil {
		return err
	}
	result.SeriesUpserted++

	if err := restoreCoverIndex(ctx, client, series, sf.Cover); err != nil {
		return err
	}

	if err := restoreMetadataIndex(ctx, client, series, sf.Metadata); err != nil {
		return err
	}

	if err := restoreTrackBindings(ctx, client, series.ID, sf.TrackBindings); err != nil {
		return err
	}

	providerIDs, err := upsertProviders(ctx, client, series.ID, sf.Chapters, result)
	if err != nil {
		return err
	}

	return reconcileChapters(ctx, client, series.ID, providerIDs, sf.Chapters, result)
}

// restoreCoverIndex re-points the series' cover fast-index (cover_file /
// cover_source_url) at the cover the sidecar records.
//
// DIRECTION IS ONE-WAY: the sidecar is the durable seed and always WINS; the DB
// columns are a derived cache of it. The DB can never legitimately be ahead —
// every writer (series.Service.fetchAndCacheCover) writes the file + sidecar
// FIRST and only then indexes — so disk→DB is the only direction reconcile may
// carry, and a DB loss must not cost the owner a re-fetch of an image already
// sitting in the series folder.
//
// It restores the FILE and its SOURCE URL only: cover_version is a hash of the
// bytes and the sidecar does not carry one, so it is left empty and re-derived by
// the next serve (series.CoverBytes hashes what it reads and re-indexes). Until
// then the series' URL is simply unversioned ⇒ served no-cache, never immutable.
//
// A series with no cached cover (cover == nil) is left alone, and an
// already-correct index is not rewritten (reconcile stays idempotent).
func restoreCoverIndex(ctx context.Context, client *ent.Client, series *ent.Series, cover *CoverProvenance) error {
	if cover == nil || cover.File == "" {
		return nil
	}
	if series.CoverFile == cover.File && series.CoverSourceURL == cover.SourceURL {
		return nil
	}
	err := client.Series.UpdateOne(series).
		SetCoverFile(cover.File).
		SetCoverSourceURL(cover.SourceURL).
		Exec(ctx)
	if err != nil {
		// Defensive path: reachable only on DB connection loss mid-run.
		return fmt.Errorf("disk.Reconcile: update cover index (series=%s): %w", series.ID, err)
	}
	return nil
}

// restoreMetadataIndex writes the Phase-1 metadata engine's rich-card block
// (see disk.SeriesMetadataSidecar) back onto the Series row's jsonb columns.
//
// DIRECTION IS ONE-WAY, mirroring restoreCoverIndex: the sidecar is the
// durable seed and always WINS — every writer (the metadata engine's
// identify/AutoIdentify path) writes the sidecar block FIRST and only then
// updates these columns, so disk→DB is the only direction Reconcile may
// carry. This is the load-bearing durability proof: after a total DB loss,
// Reconcile rebuilds the WHOLE rich card (genres/tags/alt_titles/authors/
// links/year/description/status/metadata_source/cover_source) from disk with
// ZERO calls to any metadata provider — Reconcile takes no provider argument
// at all, so a provider call during reconcile is structurally impossible.
//
// A series with no metadata block on disk (metadata == nil, the common case
// for the vast majority of series that predate this feature or were never
// identified) is left entirely alone — every new field keeps its zero value,
// exactly as an additive/defaulted column requires for a zero-data migration.
func restoreMetadataIndex(ctx context.Context, client *ent.Client, series *ent.Series, meta *SeriesMetadataSidecar) error {
	if meta == nil {
		return nil
	}
	update := client.Series.UpdateOne(series).
		SetDescription(meta.Description).
		SetStatus(meta.Status).
		SetGenres(meta.Genres).
		SetTags(meta.Tags).
		SetAltTitles(meta.AltTitles).
		SetAuthors(meta.Authors).
		SetLinks(meta.Links).
		SetYear(meta.Year)
	// The two descriptors are pointer-typed and genuinely nil-able (not yet
	// identified against a provider) — Clear* stores a real SQL NULL, so a
	// nil pointer round-trips as nil rather than a stored JSON "null" literal.
	if meta.MetadataSource != nil {
		update = update.SetMetadataSource(meta.MetadataSource)
	} else {
		update = update.ClearMetadataSource()
	}
	if meta.CoverSource != nil {
		update = update.SetCoverSource(meta.CoverSource)
	} else {
		update = update.ClearCoverSource()
	}
	if err := update.Exec(ctx); err != nil {
		// Defensive path: reachable only on DB connection loss mid-run.
		return fmt.Errorf("disk.Reconcile: update metadata index (series=%s): %w", series.ID, err)
	}
	return nil
}

// restoreTrackBindings upserts a TrackBinding row per sidecar entry in
// bindings, restoring which tracker entries seriesID was bound to and its
// last-known progress after a total DB loss
// (spec/trackers-oauth-phase3 §3/§5).
//
// DIRECTION IS ONE-WAY, mirroring restoreMetadataIndex/restoreCoverIndex:
// the sidecar is the durable seed and always WINS — bind.Service writes the
// sidecar block FIRST (via disk.WriteTrackBindings) and only then upserts
// the DB row, so disk→DB is the only direction Reconcile may carry. 🔴
// Tokens are NEVER restored here (TrackBindingSidecar carries none — see
// its own doc comment); only the binding identity + a progress snapshot.
// The binding's TrackerConnection (account token) is a SEPARATE, non-
// sidecar'd table this function never touches — a bound series with no
// reconnected account simply cannot FetchTrack until the owner re-logs in,
// exactly like any other post-wipe tracker state.
//
// A series with no bindings on disk (the common case — most series predate
// this feature or were never bound) is left entirely alone: every new
// TrackBinding row's fields keep their zero value, and no row is created,
// exactly as an additive feature requires for a zero-data migration.
func restoreTrackBindings(ctx context.Context, client *ent.Client, seriesID uuid.UUID, bindings []TrackBindingSidecar) error {
	for _, b := range bindings {
		if err := upsertTrackBinding(ctx, client, seriesID, b); err != nil {
			return err
		}
	}
	return nil
}

// upsertTrackBinding finds or creates the TrackBinding row for
// (seriesID, b.TrackerID) and writes b's fields onto it — the
// query-then-create/update pattern this package's other restore helpers
// use (mirrors restoreCoverIndex/resolveCategoryID).
func upsertTrackBinding(ctx context.Context, client *ent.Client, seriesID uuid.UUID, b TrackBindingSidecar) error {
	existing, err := client.TrackBinding.Query().
		Where(enttrackbinding.SeriesID(seriesID), enttrackbinding.TrackerID(b.TrackerID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		// Defensive path: reachable only on DB connection loss or cancelled context.
		return fmt.Errorf("disk.Reconcile: query TrackBinding (series=%s tracker=%d): %w", seriesID, b.TrackerID, err)
	}

	if existing != nil {
		_, err := existing.Update().
			SetRemoteID(b.RemoteID).
			SetRemoteURL(b.RemoteURL).
			SetStatus(b.Status).
			SetLastChapterRead(b.LastChapterRead).
			SetScore(b.Score).
			Save(ctx)
		if err != nil {
			// Defensive path: reachable only on DB connection loss mid-run.
			return fmt.Errorf("disk.Reconcile: update TrackBinding (series=%s tracker=%d): %w", seriesID, b.TrackerID, err)
		}
		return nil
	}

	_, err = client.TrackBinding.Create().
		SetSeriesID(seriesID).
		SetTrackerID(b.TrackerID).
		SetRemoteID(b.RemoteID).
		SetRemoteURL(b.RemoteURL).
		SetStatus(b.Status).
		SetLastChapterRead(b.LastChapterRead).
		SetScore(b.Score).
		Save(ctx)
	if err != nil {
		// Defensive path: reachable only on DB connection loss or a concurrent
		// INSERT that races with the query above.
		return fmt.Errorf("disk.Reconcile: create TrackBinding (series=%s tracker=%d): %w", seriesID, b.TrackerID, err)
	}
	return nil
}

// upsertSeries finds the Series row by slug or creates it, restoring the library
// category from disk so a reconcile after a recategorize (or after a DB loss) is
// a no-op (the M3 lossless-round-trip guarantee, now over the Category table).
//
// category is the on-disk category (folder name / sidecar), e.g. "Manhwa". The
// dynamic scanner reports EVERY top-level storage subdir as a category, so any
// user-named folder round-trips: the category is find-or-created by name
// (mirroring findOrCreateSeriesProvider) and linked via category_id. category is
// "" only for an orphan series directly under the storage root (no category dir,
// no sidecar category): on CREATE it defaults to the owner-chosen is_default
// category (resolveDefaultCategoryID) so a series is never left category-less; on
// UPDATE an empty disk category is ignored so it never clobbers a real category.
//
// Returns the existing or newly created row.
func upsertSeries(ctx context.Context, client *ent.Client, title, diskCategory string) (*ent.Series, error) {
	slug := Slugify(title)

	series, err := client.Series.Query().
		Where(entseries.Slug(slug)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		// Defensive path: reachable only when the DB connection is lost or the
		// context is cancelled between scans.
		return nil, fmt.Errorf("disk.Reconcile: query series %q: %w", slug, err)
	}
	if ent.IsNotFound(err) {
		return createSeriesFromDisk(ctx, client, title, slug, diskCategory)
	}

	// Update title (and category, when the disk reports one) in case they
	// changed; slug stays fixed.
	update := client.Series.UpdateOne(series).SetTitle(title)
	if diskCategory != "" {
		catID, cErr := resolveCategoryID(ctx, client, diskCategory)
		if cErr != nil {
			return nil, cErr
		}
		update = update.SetCategoryID(catID)
	}
	series, err = update.Save(ctx)
	if err != nil {
		// Defensive path: reachable only on DB connection loss mid-run.
		return nil, fmt.Errorf("disk.Reconcile: update series %q: %w", slug, err)
	}
	return series, nil
}

// createSeriesFromDisk creates a new Series row, linking it to the disk category
// (or the configured default category when the series sits uncategorized directly
// under the storage root) so every series always has a category.
//
// The empty-diskCategory fallback resolves the owner-chosen default (is_default),
// not the hardcoded "Other" — but does so via Ent directly (resolveDefaultCategoryID)
// so the disk package never imports internal/category (the dependency stays
// one-directional: category → disk).
func createSeriesFromDisk(ctx context.Context, client *ent.Client, title, slug, diskCategory string) (*ent.Series, error) {
	var catID uuid.UUID
	if diskCategory != "" {
		id, err := resolveCategoryID(ctx, client, diskCategory)
		if err != nil {
			return nil, err
		}
		catID = id
	} else {
		id, err := resolveDefaultCategoryID(ctx, client)
		if err != nil {
			return nil, err
		}
		catID = id
	}
	series, err := client.Series.Create().
		SetTitle(title).
		SetSlug(slug).
		SetCategoryID(catID).
		Save(ctx)
	if err != nil {
		// Defensive path: reachable only on DB connection loss or a concurrent
		// INSERT that wins the slug unique constraint race.
		return nil, fmt.Errorf("disk.Reconcile: create series %q: %w", slug, err)
	}
	return series, nil
}

// resolveCategoryID find-or-creates the Category for a disk folder name and
// returns its id. This is the dynamic-scanner seam: any category folder present
// on disk is materialised as a Category row so it survives a DB-loss reconcile.
//
// It find-or-creates the Category row directly via Ent (not through the category
// domain service) so the disk package does NOT import internal/category — that
// package imports disk for RenameCategory, and the dependency must stay
// one-directional (category → disk). It mirrors findOrCreateSeriesProvider:
// query-then-create, absorbing the unique-name race by re-querying.
func resolveCategoryID(ctx context.Context, client *ent.Client, name string) (uuid.UUID, error) {
	existing, err := client.Category.Query().Where(entcategory.Name(name)).First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		// Defensive path: reachable only on DB connection loss or cancelled context.
		return uuid.Nil, fmt.Errorf("disk.Reconcile: query category %q: %w", name, err)
	}
	if existing != nil {
		return existing.ID, nil
	}
	created, err := client.Category.Create().SetName(name).Save(ctx)
	if err == nil {
		return created.ID, nil
	}
	if !ent.IsConstraintError(err) {
		// Defensive path: reachable only on DB connection loss.
		return uuid.Nil, fmt.Errorf("disk.Reconcile: create category %q: %w", name, err)
	}
	// Lost the unique-name race with a concurrent create — re-query.
	row, qErr := client.Category.Query().Where(entcategory.Name(name)).Only(ctx)
	if qErr != nil {
		// Defensive path: reachable only on DB connection loss after the race.
		return uuid.Nil, fmt.Errorf("disk.Reconcile: re-query category %q: %w", name, qErr)
	}
	return row.ID, nil
}

// resolveDefaultCategoryID returns the id of the configured default category (the
// single row with is_default=true) — the landing for an orphan series with no
// on-disk category folder. It queries Ent directly (never internal/category) so
// the one-directional category → disk dependency is preserved, and falls back to
// the "Other" folder name only if no default is set (an unseeded DB — startup
// EnsureDefaults guarantees one exists in production).
func resolveDefaultCategoryID(ctx context.Context, client *ent.Client) (uuid.UUID, error) {
	row, err := client.Category.Query().Where(entcategory.IsDefault(true)).First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		// Defensive path: reachable only on DB connection loss or cancelled context.
		return uuid.Nil, fmt.Errorf("disk.Reconcile: query default category: %w", err)
	}
	if row != nil {
		return row.ID, nil
	}
	// No default set (unseeded DB) — fall back to find-or-creating "Other".
	return resolveCategoryID(ctx, client, CategoryOther)
}

// providerKey identifies a distinct SeriesProvider row on disk: the
// (provider, scanlator) pair. Two files that share a provider but carry
// different scanlators (e.g. "[Comix-Alpha]…" and "[Comix-Beta]…") must
// reconcile into TWO SeriesProvider rows, not one — collapsing them would
// lose the scanlator round-trip on a DB-loss reconcile.
type providerKey struct {
	provider  string
	scanlator string
}

// upsertProviders builds a providerKey→SeriesProvider.ID map for all distinct
// (provider, scanlator) pairs referenced in chapters, finding or creating each
// SeriesProvider row.
func upsertProviders(
	ctx context.Context,
	client *ent.Client,
	seriesID uuid.UUID,
	chapters []ChapterFact,
	result *ReconcileResult,
) (map[providerKey]uuid.UUID, error) {
	// Collect distinct (provider, scanlator) pairs with their maximum importance.
	provImportance := make(map[providerKey]int)
	for _, cf := range chapters {
		if cf.Provider == "" {
			continue
		}
		key := providerKey{provider: cf.Provider, scanlator: cf.Scanlator}
		if cf.Importance > provImportance[key] {
			provImportance[key] = cf.Importance
		}
	}

	providerIDs := make(map[providerKey]uuid.UUID, len(provImportance))
	for key, importance := range provImportance {
		sp, err := findOrCreateSeriesProvider(ctx, client, seriesID, key.provider, key.scanlator, importance)
		if err != nil {
			return nil, err
		}
		providerIDs[key] = sp.ID
		result.ProvidersUpserted++
	}
	return providerIDs, nil
}

// reconcileChapters upserts all chapter facts for a series.
// Chapters whose CBZ is missing are counted in result.MissingFiles.
func reconcileChapters(
	ctx context.Context,
	client *ent.Client,
	seriesID uuid.UUID,
	providerIDs map[providerKey]uuid.UUID,
	chapters []ChapterFact,
	result *ReconcileResult,
) error {
	for _, cf := range chapters {
		if !cf.FileExists {
			// A downloaded-but-missing chapter is only COUNTED, never downgraded:
			// its Chapter row stays in whatever state it holds (e.g. StateDownloaded)
			// and reconcile forces NO transition. This is deliberate (owner-ratified)
			// and upholds reconcile's "no forced transition" contract: a transient
			// scan fault (e.g. an NFS blip hiding a file) must not spuriously flip a
			// present chapter to a re-download. A consequence worth knowing: a
			// fractional part superseded under this whole will NOT auto-revert, since
			// download.revertOrphaned keys off the whole's DB state (StateDownloaded),
			// not disk presence. Recovery of a genuinely-lost file is a manual owner
			// retry, not an automatic downgrade here.
			result.MissingFiles++
			continue
		}
		if err := reconcileChapter(ctx, client, seriesID, providerIDs, cf, result); err != nil {
			return err
		}
	}
	return nil
}

// findOrCreateSeriesProvider queries for an existing SeriesProvider matching
// (series_id, provider, scanlator). If none exists it creates one with the
// supplied importance and scanlator. Returns the row either way.
//
// There is no unique index on SeriesProvider(series_id, provider, scanlator),
// so the lookup-then-create pattern is the correct idempotency strategy here
// (mirrors suwayomi.Ingest.upsertSeriesProvider's identity key).
func findOrCreateSeriesProvider(
	ctx context.Context,
	client *ent.Client,
	seriesID uuid.UUID,
	provName string,
	scanlator string,
	importance int,
) (*ent.SeriesProvider, error) {
	existing, err := client.SeriesProvider.Query().
		Where(
			entseriesprovider.SeriesID(seriesID),
			entseriesprovider.Provider(provName),
			entseriesprovider.Scanlator(scanlator),
		).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		// Defensive path: reachable only on DB connection loss or cancelled context.
		return nil, fmt.Errorf("disk.Reconcile: query SeriesProvider (series=%s provider=%q scanlator=%q): %w", seriesID, provName, scanlator, err)
	}
	if existing != nil {
		// Update importance in case it changed.
		updated, err := client.SeriesProvider.UpdateOne(existing).
			SetImportance(importance).
			Save(ctx)
		if err != nil {
			// Defensive path: reachable only on DB connection loss mid-run.
			return nil, fmt.Errorf("disk.Reconcile: update SeriesProvider (series=%s provider=%q scanlator=%q): %w", seriesID, provName, scanlator, err)
		}
		return updated, nil
	}

	sp, err := client.SeriesProvider.Create().
		SetSeriesID(seriesID).
		SetProvider(provName).
		SetScanlator(scanlator).
		SetImportance(importance).
		Save(ctx)
	if err != nil {
		// Defensive path: reachable only on DB connection loss or a concurrent
		// INSERT that races with the query above.
		return nil, fmt.Errorf("disk.Reconcile: create SeriesProvider (series=%s provider=%q scanlator=%q): %w", seriesID, provName, scanlator, err)
	}
	return sp, nil
}

// reconcileChapter finds or creates the Chapter row for cf, setting its state
// to downloaded and filling in all provenance fields.
func reconcileChapter(
	ctx context.Context,
	client *ent.Client,
	seriesID uuid.UUID,
	providerIDs map[providerKey]uuid.UUID,
	cf ChapterFact,
	result *ReconcileResult,
) error {
	spID, hasProvider := providerIDs[providerKey{provider: cf.Provider, scanlator: cf.Scanlator}]

	existing, err := client.Chapter.Query().
		Where(
			entchapter.SeriesID(seriesID),
			entchapter.ChapterKey(cf.Key),
		).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		// Defensive path: reachable only on DB connection loss or cancelled context.
		return fmt.Errorf("disk.Reconcile: query Chapter (series=%s key=%q): %w", seriesID, cf.Key, err)
	}

	if ent.IsNotFound(err) {
		return adoptChapter(ctx, client, seriesID, spID, hasProvider, cf, result)
	}

	return updateChapter(ctx, client, existing, spID, hasProvider, cf, result)
}

// adoptChapter creates a new Chapter row from on-disk provenance (no prior DB row).
func adoptChapter(
	ctx context.Context,
	client *ent.Client,
	seriesID uuid.UUID,
	spID uuid.UUID,
	hasProvider bool,
	cf ChapterFact,
	result *ReconcileResult,
) error {
	create := client.Chapter.Create().
		SetSeriesID(seriesID).
		SetChapterKey(cf.Key).
		SetState(entchapter.StateDownloaded).
		SetFilename(cf.Filename).
		SetNillableNumber(cf.Number).
		SetNillablePageCount(&cf.PageCount).
		SetSatisfiedImportance(cf.Importance)
	if hasProvider {
		create = create.SetSatisfiedByProviderID(spID)
	}
	created, err := create.Save(ctx)
	if err != nil {
		// Defensive path: reachable only on DB connection loss or a concurrent
		// INSERT that races with the query above.
		return fmt.Errorf("disk.Reconcile: create Chapter (series=%s key=%q): %w", seriesID, cf.Key, err)
	}
	if err := seedFirstDownloadedAtFromMtime(ctx, client, created.ID, cf.ModTime); err != nil {
		return err
	}
	result.ChaptersAdopted++
	result.ChaptersUpserted++
	return nil
}

// seedFirstDownloadedAtFromMtime backfills a disk-imported chapter's arrival
// time from its CBZ's mtime — the only real evidence of when it became
// readable (a Kaizoku import has no download_date and never will).
//
// WRITE-ONCE via the SAME predicate-guarded pattern the download dispatcher
// uses (dispatcher.go finishDownload): the FirstDownloadedAtIsNil() predicate,
// not Go control flow, is what enforces it — no read-modify-write, no race.
// This is deliberate: a chapter Tsundoku itself downloaded already carries the
// authoritative value, and a convergence upgrade REWRITES the CBZ (and
// therefore its mtime) when it re-fetches an old chapter from a better source.
// Trusting mtime over an existing value here would re-introduce the exact bug
// first_downloaded_at exists to kill. A zero ModTime (unknown — a stat failed
// mid-scan) is skipped entirely, never written as a bogus value.
func seedFirstDownloadedAtFromMtime(ctx context.Context, client *ent.Client, chapterID uuid.UUID, modTime time.Time) error {
	if modTime.IsZero() {
		return nil
	}
	if _, err := client.Chapter.Update().
		Where(entchapter.ID(chapterID), entchapter.FirstDownloadedAtIsNil()).
		SetFirstDownloadedAt(modTime).
		Save(ctx); err != nil {
		// Defensive path: reachable only on DB connection loss mid-run.
		return fmt.Errorf("disk.Reconcile: seed first_downloaded_at for chapter %s: %w", chapterID, err)
	}
	return nil
}

// updateChapter updates an existing Chapter row with the latest provenance from disk.
func updateChapter(
	ctx context.Context,
	client *ent.Client,
	existing *ent.Chapter,
	spID uuid.UUID,
	hasProvider bool,
	cf ChapterFact,
	result *ReconcileResult,
) error {
	update := client.Chapter.UpdateOne(existing).
		SetState(entchapter.StateDownloaded).
		SetFilename(cf.Filename).
		SetNillableNumber(cf.Number).
		SetNillablePageCount(&cf.PageCount).
		SetSatisfiedImportance(cf.Importance)
	if hasProvider {
		update = update.SetSatisfiedByProviderID(spID)
	}
	if _, err := update.Save(ctx); err != nil {
		// Defensive path: reachable only on DB connection loss mid-run.
		return fmt.Errorf("disk.Reconcile: update Chapter (series=%s key=%q): %w", existing.SeriesID, cf.Key, err)
	}
	if err := seedFirstDownloadedAtFromMtime(ctx, client, existing.ID, cf.ModTime); err != nil {
		return err
	}
	result.ChaptersUpserted++
	return nil
}
