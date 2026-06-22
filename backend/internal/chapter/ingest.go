package chapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
)

// FetchedChapter is the raw chapter data supplied by a provider before ingest.
// Number is nil for un-numbered chapters (named volumes, specials, etc.).
// ProviderIndex is the position in the provider's chapter list and is used for
// ordering when numeric chapter numbers are absent or ambiguous.
type FetchedChapter struct {
	Number        *float64
	Name          string
	URL           string
	ProviderIndex int
	PageCount     *int
	UploadDate    *time.Time
}

// IngestResult reports how many new rows were created during an ingest call.
// Only genuinely-new rows are counted; existing rows that were re-fetched after
// a unique-constraint race do not increment either counter.
type IngestResult struct {
	NewChapters         int
	NewProviderChapters int
}

// IngestProviderChapters processes a slice of provider-supplied chapters for a
// given SeriesProvider, creating or upserting the corresponding ProviderChapter
// rows and ensuring that exactly one Chapter row exists per (series_id,
// chapter_key) pair.
//
// For each FetchedChapter:
//  1. chapter_key is derived via NormalizeChapterKey (Task 1's normaliser).
//  2. The ProviderChapter row keyed (series_provider_id, chapter_key) is created
//     or updated in-place (all mutable fields are refreshed on conflict).
//  3. A Chapter row keyed (series_id, chapter_key) is created with state=wanted
//     if it does not yet exist. On a concurrent INSERT race the constraint error
//     is absorbed and the existing row is re-fetched — no error is surfaced to
//     the caller for this path.
//
// Returns an IngestResult counting the rows that were genuinely new, and any
// non-dedup error encountered during the operation.
func IngestProviderChapters(
	ctx context.Context,
	client *ent.Client,
	seriesProviderID uuid.UUID,
	chapters []FetchedChapter,
) (IngestResult, error) {
	sp, err := client.SeriesProvider.Get(ctx, seriesProviderID)
	if err != nil {
		return IngestResult{}, fmt.Errorf("chapter.IngestProviderChapters: load series provider %s: %w", seriesProviderID, err)
	}
	seriesID := sp.SeriesID

	var result IngestResult

	for _, fc := range chapters {
		key := NormalizeChapterKey(fc.Number, fc.Name)

		newPC, err := ingestProviderChapter(ctx, client, seriesProviderID, key, fc)
		if err != nil {
			return IngestResult{}, fmt.Errorf("chapter.IngestProviderChapters: provider chapter %q: %w", key, err)
		}
		if newPC {
			result.NewProviderChapters++
		}

		newCh, err := ensureChapter(ctx, client, seriesID, key, fc.Number)
		if err != nil {
			return IngestResult{}, fmt.Errorf("chapter.IngestProviderChapters: chapter %q: %w", key, err)
		}
		if newCh {
			result.NewChapters++
		}
	}

	return result, nil
}

// ingestProviderChapter creates or updates the ProviderChapter row for
// (seriesProviderID, chapterKey). Returns true when a new row was inserted.
func ingestProviderChapter(
	ctx context.Context,
	client *ent.Client,
	seriesProviderID uuid.UUID,
	key string,
	fc FetchedChapter,
) (isNew bool, err error) {
	// Try to fetch the existing row first (read-before-write keeps the common
	// re-ingest path cheap and avoids a write on every sync).
	existing, err := client.ProviderChapter.Query().
		Where(
			entproviderchapter.SeriesProviderID(seriesProviderID),
			entproviderchapter.ChapterKey(key),
		).
		Only(ctx)

	if err == nil {
		// Row exists — update all mutable fields in place.
		if _, err := applyProviderChapterUpdate(ctx, client, existing.ID, fc); err != nil {
			return false, fmt.Errorf("update: %w", err)
		}
		return false, nil
	}

	if !ent.IsNotFound(err) {
		return false, fmt.Errorf("query: %w", err)
	}

	// Row does not exist — insert it.
	_, err = client.ProviderChapter.Create().
		SetSeriesProviderID(seriesProviderID).
		SetChapterKey(key).
		SetNillableNumber(fc.Number).
		SetName(fc.Name).
		SetURL(fc.URL).
		SetProviderIndex(fc.ProviderIndex).
		SetNillableProviderUploadDate(fc.UploadDate).
		SetNillablePageCount(fc.PageCount).
		Save(ctx)
	if err == nil {
		return true, nil
	}

	// Absorb a unique-constraint race — a concurrent goroutine beat us to the
	// INSERT. Re-fetch the existing row and apply our values via UPDATE so the
	// caller never sees a constraint error.
	if ent.IsConstraintError(err) {
		return false, absorbProviderChapterRace(ctx, client, seriesProviderID, key, fc)
	}

	return false, fmt.Errorf("insert: %w", err)
}

// absorbProviderChapterRace handles the concurrent-INSERT race for ProviderChapter:
// re-fetches the winner's row and updates it with the current call's values.
func absorbProviderChapterRace(
	ctx context.Context,
	client *ent.Client,
	seriesProviderID uuid.UUID,
	key string,
	fc FetchedChapter,
) error {
	existing, err := client.ProviderChapter.Query().
		Where(
			entproviderchapter.SeriesProviderID(seriesProviderID),
			entproviderchapter.ChapterKey(key),
		).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("re-fetch after constraint race: %w", err)
	}
	if _, err := applyProviderChapterUpdate(ctx, client, existing.ID, fc); err != nil {
		return fmt.Errorf("update after constraint race: %w", err)
	}
	return nil
}

// applyProviderChapterUpdate sets all mutable fields on an existing ProviderChapter
// row, clearing optional fields when the corresponding FetchedChapter field is nil.
func applyProviderChapterUpdate(
	ctx context.Context,
	client *ent.Client,
	id uuid.UUID,
	fc FetchedChapter,
) (*ent.ProviderChapter, error) {
	upd := client.ProviderChapter.UpdateOneID(id).
		SetNillableNumber(fc.Number).
		SetName(fc.Name).
		SetURL(fc.URL).
		SetProviderIndex(fc.ProviderIndex).
		SetNillableProviderUploadDate(fc.UploadDate).
		SetNillablePageCount(fc.PageCount)
	if fc.Number == nil {
		upd = upd.ClearNumber()
	}
	if fc.UploadDate == nil {
		upd = upd.ClearProviderUploadDate()
	}
	if fc.PageCount == nil {
		upd = upd.ClearPageCount()
	}
	return upd.Save(ctx)
}

// ensureChapter guarantees that exactly one Chapter row exists for
// (seriesID, chapterKey). If an INSERT races with a concurrent INSERT for the
// same key, the constraint error is absorbed and the existing row is returned;
// no error is surfaced for this path. Returns true when a new row was created.
func ensureChapter(
	ctx context.Context,
	client *ent.Client,
	seriesID uuid.UUID,
	key string,
	number *float64,
) (isNew bool, err error) {
	_, err = client.Chapter.Create().
		SetSeriesID(seriesID).
		SetChapterKey(key).
		SetNillableNumber(number).
		SetState(entchapter.StateWanted).
		Save(ctx)

	if err == nil {
		return true, nil
	}

	// Absorb a unique-constraint race — re-fetch to confirm the row exists.
	if ent.IsConstraintError(err) {
		_, ferr := client.Chapter.Query().
			Where(
				entchapter.SeriesID(seriesID),
				entchapter.ChapterKey(key),
			).
			Only(ctx)
		if ferr != nil {
			// Defensive path: the row was deleted between our constraint error and
			// this re-fetch. This cannot happen under normal operation (rows are
			// never deleted mid-ingest). Documented per engineering standard —
			// unreachable branches are documented, not faked.
			return false, fmt.Errorf("re-fetch after constraint race: %w", ferr)
		}
		return false, nil
	}

	return false, fmt.Errorf("insert: %w", err)
}
