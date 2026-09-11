package chapter

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	Number *float64
	Name   string
	URL    string
	// WebURL is the fully-qualified, browser-clickable chapter URL (Mihon's
	// HttpSource.getChapterUrl, surfaced end-to-end as
	// sourceengine.Chapter.RealURL) — feeds Komga's ComicInfo <Web> field
	// (disk.RenderMeta.WebURL). Distinct in purpose from URL (the source-owned
	// serialized address) — never used for identity/dedup. The values may be
	// equal when that address is already the browser URL. "" when the source
	// could not resolve one.
	WebURL        string
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

// ProviderChapterCacheInvalidator starts a recoverable cache-invalidation
// transaction. Its transaction must quarantine staged bytes until Commit and
// restore them on Rollback, keeping filesystem state aligned with the database
// transaction that clears page links.
type ProviderChapterCacheInvalidator interface {
	BeginProviderChapterCacheInvalidation() ProviderChapterCacheInvalidation
}

// ProviderChapterCacheInvalidation is one recoverable staging-cache journal.
// Invalidate removes a cache from live use, Commit discards its quarantined
// bytes, and Rollback restores them.
type ProviderChapterCacheInvalidation interface {
	InvalidateProviderChapterCache(context.Context, uuid.UUID) error
	Commit()
	Rollback() error
}

type noopProviderChapterCacheInvalidation struct{}

func (noopProviderChapterCacheInvalidation) InvalidateProviderChapterCache(context.Context, uuid.UUID) error {
	return nil
}

func (noopProviderChapterCacheInvalidation) Commit() {}

func (noopProviderChapterCacheInvalidation) Rollback() error { return nil }

// ProviderReconcileScope serializes one SeriesProvider feed reconcile against
// other reconciles and active download fetches. Transactional callers keep the
// scope open until their Ent transaction commits or rolls back.
type ProviderReconcileScope struct {
	providerID    uuid.UUID
	lease         *ProviderFeedLease
	chapterLeases map[uuid.UUID]*ProviderChapterLease
	invalidation  ProviderChapterCacheInvalidation
	done          sync.Once
	rollbackErr   error
}

// BeginProviderReconcile acquires exclusive ownership of providerID's feed and
// starts its recoverable staging-cache journal.
func BeginProviderReconcile(
	ctx context.Context,
	providerID uuid.UUID,
	invalidator ProviderChapterCacheInvalidator,
) (*ProviderReconcileScope, error) {
	lease, err := AcquireProviderFeedLease(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("chapter.BeginProviderReconcile: acquire provider feed: %w", err)
	}
	invalidation := ProviderChapterCacheInvalidation(noopProviderChapterCacheInvalidation{})
	if invalidator != nil {
		invalidation = invalidator.BeginProviderChapterCacheInvalidation()
		if invalidation == nil {
			lease.Release()
			return nil, errors.New("chapter.BeginProviderReconcile: cache invalidator returned nil transaction")
		}
	}
	return &ProviderReconcileScope{
		providerID:    providerID,
		lease:         lease,
		chapterLeases: make(map[uuid.UUID]*ProviderChapterLease),
		invalidation:  invalidation,
	}, nil
}

// ReconcileProviderChapters applies chapters through client while retaining the
// scope's feed ownership and cache journal. client may be tx.Client().
func (s *ProviderReconcileScope) ReconcileProviderChapters(
	ctx context.Context,
	client *ent.Client,
	chapters []FetchedChapter,
) (IngestResult, error) {
	return reconcileProviderChapters(ctx, client, s, chapters)
}

// Commit makes this scope's cache invalidations permanent and releases its feed
// ownership. Call it only after the corresponding database commit succeeds.
func (s *ProviderReconcileScope) Commit() {
	if s == nil {
		return
	}
	s.done.Do(func() {
		s.invalidation.Commit()
		s.releaseLeases()
	})
}

// Rollback restores this scope's quarantined caches and releases its feed
// ownership. Call it only after the corresponding database rollback completes.
func (s *ProviderReconcileScope) Rollback() error {
	if s == nil {
		return nil
	}
	s.done.Do(func() {
		s.rollbackErr = s.invalidation.Rollback()
		s.releaseLeases()
	})
	return s.rollbackErr
}

func (s *ProviderReconcileScope) lockProviderChapter(
	ctx context.Context,
	client *ent.Client,
	providerChapterID uuid.UUID,
) (*ent.ProviderChapter, error) {
	if s.chapterLeases[providerChapterID] == nil {
		lease, err := AcquireProviderChapterLease(ctx, providerChapterID)
		if err != nil {
			return nil, err
		}
		s.chapterLeases[providerChapterID] = lease
	}
	return client.ProviderChapter.Get(ctx, providerChapterID)
}

func (s *ProviderReconcileScope) releaseLeases() {
	for _, lease := range s.chapterLeases {
		lease.Release()
	}
	s.lease.Release()
}

// IngestProviderChapters processes a slice of provider-supplied chapters for a
// given SeriesProvider, creating or upserting the corresponding ProviderChapter
// rows and ensuring that exactly one Chapter row exists per (series_id,
// chapter_key) pair.
//
// For each FetchedChapter:
//  1. chapter_key is derived via NormalizeChapterKey.
//  2. The ProviderChapter row keyed (series_provider_id, chapter_key) is created
//     or updated in-place (all mutable fields are refreshed on conflict).
//  3. A Chapter row keyed (series_id, chapter_key) is created with state=wanted
//     if it does not yet exist. Concurrent inserts use transaction-safe
//     ON CONFLICT handling, so the structural winner is retained without
//     aborting an enclosing transaction.
//
// Returns an IngestResult counting the rows that were genuinely new, and any
// non-dedup error encountered during the operation.
func IngestProviderChapters(
	ctx context.Context,
	client *ent.Client,
	seriesProviderID uuid.UUID,
	chapters []FetchedChapter,
) (IngestResult, error) {
	return ReconcileProviderChapters(ctx, client, seriesProviderID, chapters, nil)
}

// ReconcileProviderChapters atomically reconciles a provider feed using a new
// Ent transaction and feed scope. When an existing ProviderChapter's URL or
// WebURL changes, staged bytes are quarantined before page_links is cleared;
// commit discards them and rollback restores them. Callers already holding an
// Ent transaction use BeginProviderReconcile and the returned scope instead.
func ReconcileProviderChapters(
	ctx context.Context,
	client *ent.Client,
	seriesProviderID uuid.UUID,
	chapters []FetchedChapter,
	invalidator ProviderChapterCacheInvalidator,
) (IngestResult, error) {
	scope, err := BeginProviderReconcile(ctx, seriesProviderID, invalidator)
	if err != nil {
		return IngestResult{}, err
	}
	tx, err := client.Tx(ctx)
	if err != nil {
		_ = scope.Rollback()
		return IngestResult{}, fmt.Errorf("chapter.ReconcileProviderChapters: begin transaction: %w", err)
	}
	result, err := scope.ReconcileProviderChapters(ctx, tx.Client(), chapters)
	if err != nil {
		return IngestResult{}, rollbackProviderReconcile(tx, scope, err)
	}
	if err := tx.Commit(); err != nil {
		return IngestResult{}, errors.Join(
			fmt.Errorf("chapter.ReconcileProviderChapters: commit: %w", err),
			scope.Rollback(),
		)
	}
	scope.Commit()
	return result, nil
}

func rollbackProviderReconcile(tx *ent.Tx, scope *ProviderReconcileScope, cause error) error {
	return errors.Join(cause, tx.Rollback(), scope.Rollback())
}

func reconcileProviderChapters(
	ctx context.Context,
	client *ent.Client,
	scope *ProviderReconcileScope,
	chapters []FetchedChapter,
) (IngestResult, error) {
	seriesProviderID := scope.providerID
	sp, err := client.SeriesProvider.Get(ctx, seriesProviderID)
	if err != nil {
		return IngestResult{}, fmt.Errorf("chapter.IngestProviderChapters: load series provider %s: %w", seriesProviderID, err)
	}
	seriesID := sp.SeriesID

	var result IngestResult

	for _, fc := range chapters {
		key := NormalizeChapterKey(fc.Number, fc.Name)

		newPC, err := ingestProviderChapter(ctx, client, scope, key, fc)
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
	scope *ProviderReconcileScope,
	key string,
	fc FetchedChapter,
) (isNew bool, err error) {
	seriesProviderID := scope.providerID
	// Try to fetch the existing row first (read-before-write keeps the common
	// re-ingest path cheap and avoids a write on every sync).
	existing, err := client.ProviderChapter.Query().
		Where(
			entproviderchapter.SeriesProviderID(seriesProviderID),
			entproviderchapter.ChapterKey(key),
		).
		Only(ctx)

	if err == nil {
		existing, err = scope.lockProviderChapter(ctx, client, existing.ID)
		if err != nil {
			return false, fmt.Errorf("lock and refresh: %w", err)
		}
		// Row exists — update all mutable fields in place.
		if _, err := applyProviderChapterUpdate(ctx, client, existing, fc, scope.invalidation); err != nil {
			return false, fmt.Errorf("update: %w", err)
		}
		return false, nil
	}

	if !ent.IsNotFound(err) {
		// Defensive path: real DB error (e.g. connection lost) during the initial
		// read. Not reachable under normal operation — only a mid-operation failure
		// of the database layer would reach this branch.
		return false, fmt.Errorf("query: %w", err)
	}

	// Row does not exist — insert it. ON CONFLICT keeps a concurrent winner from
	// aborting an enclosing PostgreSQL transaction.
	createdID := uuid.New()
	err = client.ProviderChapter.Create().
		SetID(createdID).
		SetSeriesProviderID(seriesProviderID).
		SetChapterKey(key).
		SetNillableNumber(fc.Number).
		SetName(fc.Name).
		SetURL(fc.URL).
		SetWebURL(fc.WebURL).
		SetProviderIndex(fc.ProviderIndex).
		SetNillableProviderUploadDate(fc.UploadDate).
		SetNillablePageCount(fc.PageCount).
		OnConflictColumns(entproviderchapter.FieldSeriesProviderID, entproviderchapter.FieldChapterKey).
		Ignore().
		Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("insert: %w", err)
	}
	created, err := client.ProviderChapter.Query().Where(entproviderchapter.ID(createdID)).Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("verify insert: %w", err)
	}
	if created {
		return true, nil
	}

	// A concurrent insert won the structural key. Refresh that same row in place.
	return false, absorbProviderChapterRace(ctx, client, scope, key, fc)
}

// absorbProviderChapterRace handles a concurrent ProviderChapter insert by
// re-fetching the structural winner and updating it with the current values.
func absorbProviderChapterRace(
	ctx context.Context,
	client *ent.Client,
	scope *ProviderReconcileScope,
	key string,
	fc FetchedChapter,
) error {
	seriesProviderID := scope.providerID
	existing, err := client.ProviderChapter.Query().
		Where(
			entproviderchapter.SeriesProviderID(seriesProviderID),
			entproviderchapter.ChapterKey(key),
		).
		Only(ctx)
	if err != nil {
		// Defensive path: the winner's row vanished between the ignored conflict
		// and this re-fetch. Since M6, this is reachable in principle: a concurrent
		// owner-initiated RemoveProvider (HTTP goroutine) can delete the
		// ProviderChapter row after the insert observed the conflict but before
		// this re-fetch executes. The error is handled gracefully
		// (returned to the caller, never panics). Tested by
		// TestAbsorbProviderChapterRaceVanishedRow.
		return fmt.Errorf("re-fetch after constraint race: %w", err)
	}
	existing, err = scope.lockProviderChapter(ctx, client, existing.ID)
	if err != nil {
		return fmt.Errorf("lock and refresh after constraint race: %w", err)
	}
	if _, err := applyProviderChapterUpdate(ctx, client, existing, fc, scope.invalidation); err != nil {
		// Defensive path: DB connection lost between re-fetch and update — not
		// reachable under normal operation.
		return fmt.Errorf("update after constraint race: %w", err)
	}
	return nil
}

// applyProviderChapterUpdate sets all mutable fields on an existing ProviderChapter
// row, clearing optional fields when the corresponding FetchedChapter field is nil.
func applyProviderChapterUpdate(
	ctx context.Context,
	client *ent.Client,
	existing *ent.ProviderChapter,
	fc FetchedChapter,
	invalidation ProviderChapterCacheInvalidation,
) (*ent.ProviderChapter, error) {
	addressChanged := existing.URL != fc.URL || existing.WebURL != fc.WebURL
	if addressChanged {
		if err := invalidation.InvalidateProviderChapterCache(ctx, existing.ID); err != nil {
			return nil, fmt.Errorf("invalidate resolver cache: %w", err)
		}
	}
	upd := client.ProviderChapter.UpdateOneID(existing.ID).
		SetNillableNumber(fc.Number).
		SetName(fc.Name).
		SetURL(fc.URL).
		SetWebURL(fc.WebURL).
		SetProviderIndex(fc.ProviderIndex).
		SetNillableProviderUploadDate(fc.UploadDate).
		SetNillablePageCount(fc.PageCount)
	if addressChanged {
		upd = upd.ClearPageLinks()
	}
	if fc.Number == nil {
		upd = upd.ClearNumber()
	}
	if fc.UploadDate == nil {
		upd = upd.ClearProviderUploadDate()
	}
	if fc.PageCount == nil {
		upd = upd.ClearPageCount()
	}
	// Defensive path: Save error is only reachable if the DB connection is lost
	// between building the update and executing it — not reachable under normal
	// operation.
	return upd.Save(ctx)
}

// ensureChapter guarantees that exactly one Chapter row exists for
// (seriesID, chapterKey). A concurrent insert uses ON CONFLICT DO NOTHING so an
// enclosing PostgreSQL transaction remains usable. Returns true only when this
// call created the row.
func ensureChapter(
	ctx context.Context,
	client *ent.Client,
	seriesID uuid.UUID,
	key string,
	number *float64,
) (isNew bool, err error) {
	exists, err := client.Chapter.Query().
		Where(entchapter.SeriesID(seriesID), entchapter.ChapterKey(key)).
		Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("query: %w", err)
	}
	if exists {
		return false, nil
	}

	createdID := uuid.New()
	err = client.Chapter.Create().
		SetID(createdID).
		SetSeriesID(seriesID).
		SetChapterKey(key).
		SetNillableNumber(number).
		SetState(entchapter.StateWanted).
		OnConflictColumns(entchapter.FieldSeriesID, entchapter.FieldChapterKey).
		Ignore().
		Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("insert: %w", err)
	}
	created, err := client.Chapter.Query().Where(entchapter.ID(createdID)).Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("verify insert: %w", err)
	}
	return created, nil
}
