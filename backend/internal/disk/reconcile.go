package disk

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
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

// reconcileSeries upserts one series and all its providers and chapters.
func reconcileSeries(ctx context.Context, client *ent.Client, sf SeriesFacts, result *ReconcileResult) error {
	series, err := upsertSeries(ctx, client, sf.Title, sf.Category)
	if err != nil {
		return err
	}
	result.SeriesUpserted++

	providerIDs, err := upsertProviders(ctx, client, series.ID, sf.Chapters, result)
	if err != nil {
		return err
	}

	return reconcileChapters(ctx, client, series.ID, providerIDs, sf.Chapters, result)
}

// upsertSeries finds the Series row by slug or creates it, restoring the
// library category from disk so a reconcile after a recategorize is a no-op
// (the M3 lossless-round-trip guarantee).
//
// category is the on-disk category (folder name / sidecar), e.g. "Manhwa". It
// is "" for an orphan series with no category folder and no sidecar category:
// in that case the category is left untouched — on create the column default
// (Other) applies; on update the existing value is preserved — so an unknown
// disk category never clobbers a real one with an illegal empty enum. A
// non-empty but invalid category (an unrecognised folder name) is treated the
// same as empty: skipped with a logged warning, never failing the whole
// reconcile over one bad folder.
//
// Returns the existing or newly created row.
func upsertSeries(ctx context.Context, client *ent.Client, title, category string) (*ent.Series, error) {
	slug := Slugify(title)
	cat, hasCat := validCategory(ctx, category)

	series, err := client.Series.Query().
		Where(entseries.Slug(slug)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		// Defensive path: reachable only when the DB connection is lost or the
		// context is cancelled between scans.
		return nil, fmt.Errorf("disk.Reconcile: query series %q: %w", slug, err)
	}
	if ent.IsNotFound(err) {
		create := client.Series.Create().
			SetTitle(title).
			SetSlug(slug)
		if hasCat {
			create = create.SetCategory(cat)
		}
		series, err = create.Save(ctx)
		if err != nil {
			// Defensive path: reachable only on DB connection loss or a concurrent
			// INSERT that wins the slug unique constraint race.
			return nil, fmt.Errorf("disk.Reconcile: create series %q: %w", slug, err)
		}
		return series, nil
	}

	// Update title (and category) in case they changed; slug stays fixed.
	update := client.Series.UpdateOne(series).SetTitle(title)
	if hasCat {
		update = update.SetCategory(cat)
	}
	series, err = update.Save(ctx)
	if err != nil {
		// Defensive path: reachable only on DB connection loss mid-run.
		return nil, fmt.Errorf("disk.Reconcile: update series %q: %w", slug, err)
	}
	return series, nil
}

// validCategory converts an on-disk category string into a typed enum value.
// It returns (cat, true) only for a legal Series category; for an empty or
// unrecognised string it returns (_, false) so the caller skips setting the
// field. An invalid (non-empty, unrecognised) value is logged — not silently
// swallowed (§16) and not fatal: one bad library folder must not abort the
// whole reconcile.
func validCategory(ctx context.Context, category string) (entseries.Category, bool) {
	if category == "" {
		return "", false
	}
	cat := entseries.Category(category)
	if err := entseries.CategoryValidator(cat); err != nil {
		slog.WarnContext(ctx, "disk.Reconcile: ignoring unrecognised series category on disk; leaving category at its default/current value",
			"category", category,
			"err", err,
		)
		return "", false
	}
	return cat, true
}

// upsertProviders builds a provider→SeriesProvider.ID map for all distinct
// providers referenced in chapters, finding or creating each SeriesProvider row.
func upsertProviders(
	ctx context.Context,
	client *ent.Client,
	seriesID uuid.UUID,
	chapters []ChapterFact,
	result *ReconcileResult,
) (map[string]uuid.UUID, error) {
	// Collect distinct provider names with their maximum importance.
	provImportance := make(map[string]int)
	for _, cf := range chapters {
		if cf.Provider != "" && cf.Importance > provImportance[cf.Provider] {
			provImportance[cf.Provider] = cf.Importance
		}
	}

	providerIDs := make(map[string]uuid.UUID, len(provImportance))
	for provName, importance := range provImportance {
		sp, err := findOrCreateSeriesProvider(ctx, client, seriesID, provName, importance)
		if err != nil {
			return nil, err
		}
		providerIDs[provName] = sp.ID
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
	providerIDs map[string]uuid.UUID,
	chapters []ChapterFact,
	result *ReconcileResult,
) error {
	for _, cf := range chapters {
		if !cf.FileExists {
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
// (series_id, provider). If none exists it creates one with the supplied
// importance. Returns the row either way.
//
// There is no unique index on SeriesProvider(series_id, provider), so the
// lookup-then-create pattern is the correct idempotency strategy here.
func findOrCreateSeriesProvider(
	ctx context.Context,
	client *ent.Client,
	seriesID uuid.UUID,
	provName string,
	importance int,
) (*ent.SeriesProvider, error) {
	existing, err := client.SeriesProvider.Query().
		Where(
			entseriesprovider.SeriesID(seriesID),
			entseriesprovider.Provider(provName),
		).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		// Defensive path: reachable only on DB connection loss or cancelled context.
		return nil, fmt.Errorf("disk.Reconcile: query SeriesProvider (series=%s provider=%q): %w", seriesID, provName, err)
	}
	if existing != nil {
		// Update importance in case it changed.
		updated, err := client.SeriesProvider.UpdateOne(existing).
			SetImportance(importance).
			Save(ctx)
		if err != nil {
			// Defensive path: reachable only on DB connection loss mid-run.
			return nil, fmt.Errorf("disk.Reconcile: update SeriesProvider (series=%s provider=%q): %w", seriesID, provName, err)
		}
		return updated, nil
	}

	sp, err := client.SeriesProvider.Create().
		SetSeriesID(seriesID).
		SetProvider(provName).
		SetImportance(importance).
		Save(ctx)
	if err != nil {
		// Defensive path: reachable only on DB connection loss or a concurrent
		// INSERT that races with the query above.
		return nil, fmt.Errorf("disk.Reconcile: create SeriesProvider (series=%s provider=%q): %w", seriesID, provName, err)
	}
	return sp, nil
}

// reconcileChapter finds or creates the Chapter row for cf, setting its state
// to downloaded and filling in all provenance fields.
func reconcileChapter(
	ctx context.Context,
	client *ent.Client,
	seriesID uuid.UUID,
	providerIDs map[string]uuid.UUID,
	cf ChapterFact,
	result *ReconcileResult,
) error {
	spID, hasProvider := providerIDs[cf.Provider]

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
	if _, err := create.Save(ctx); err != nil {
		// Defensive path: reachable only on DB connection loss or a concurrent
		// INSERT that races with the query above.
		return fmt.Errorf("disk.Reconcile: create Chapter (series=%s key=%q): %w", seriesID, cf.Key, err)
	}
	result.ChaptersAdopted++
	result.ChaptersUpserted++
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
	result.ChaptersUpserted++
	return nil
}
