package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	entsuwayomisyncstate "github.com/technobecet/tsundoku/internal/ent/suwayomisyncstate"
	"github.com/technobecet/tsundoku/internal/series"
)

// ErrNotADiskProvider is returned by MatchDiskProvider when the target
// SeriesProvider is already a real, linked Suwayomi source (suwayomi_id != 0).
// Match only operates on unlinked disk-origin groups — see ProviderDTO.Linked.
var ErrNotADiskProvider = errors.New("provider is not a disk-origin (unlinked) provider")

// relabeledChapter records one chapter's successful disk relabel, so a later
// failure (another chapter's relabel, or the DB tx) can roll every prior one
// back via disk.UndoRelabelChapterFile.
type relabeledChapter struct {
	chapterID   uuid.UUID
	oldFilename string
	newFilename string
	oldCI       disk.ComicInfo
	oldMeta     disk.RenderMeta
}

// MatchDiskProvider attributes a series' EXISTING imported/unattributed
// on-disk chapters (currently satisfied by a disk-origin SeriesProvider —
// suwayomi_id == 0, importance 1, no ProviderChapter feed — see
// disk.kaizokuProvenance / disk.Reconcile) to a real Suwayomi source WITHOUT
// re-downloading them.
//
// This is DISTINCT from AddProvider: AddProvider attaches an ADDITIONAL
// source for new chapters/upgrades and lets the upgrade engine re-fetch
// existing chapters from it; Match instead RE-POINTS the chapters the disk
// provider already satisfies onto the newly-linked source so
// importance <= satisfied_importance holds and download.DetectUpgrades never
// fires for them — the whole point of this operation (see
// spec/library-match-and-source-management).
//
// Algorithm (disk-first + DB tx, mirrors disk.MoveSeriesCategory's
// rename-then-compensate pattern — disk ops cannot live inside a DB tx):
//  1. Load the series and the target disk provider; reject a provider that is
//     already linked (suwayomi_id != 0) with ErrNotADiskProvider.
//  2. Attach the real source via suwayomi.Ingest.AddSeries (idempotent) and
//     set its importance. A Suwayomi fetch failure is wrapped as
//     ErrSourceNotFound (mirrors AddProvider). If a later step fails, this
//     newly-attached SeriesProvider is deliberately left in place — an orphan
//     real provider is harmless and reconcile-consistent (documented, not
//     rolled back).
//  3. Compute the re-point set: chapters currently downloaded and satisfied
//     by the disk provider whose chapter_key the new source's feed also
//     offers. For each, relabel its CBZ (rename + rewrite ComicInfo + sidecar)
//     to the new source's identity via disk.RelabelChapterFile, tracking
//     (old, new) pairs. Any disk failure rolls back every relabel done so far
//     (disk.UndoRelabelChapterFile, reverse order) and returns the error with
//     NO DB change made yet.
//  4. In one all-or-nothing DB tx: re-point each relabeled chapter
//     (satisfied_by_provider_id, satisfied_importance, filename) onto the new
//     source; clear the dangling metadata_provider_id pointer if it targeted
//     the disk provider (mirrors series.RemoveProvider); clear satisfied_by
//     (keeping satisfied_importance as a watermark) on any chapters the disk
//     provider still satisfies that the new source did NOT cover (a partial
//     overlap — mirrors series.RemoveProvider's dangling-FK guard exactly, so
//     those chapters are never left pointing at a row about to be deleted);
//     delete the (now fully drained) disk provider row, its ProviderChapter
//     feed (always empty for a disk provider), and its SuwayomiSyncState. A tx
//     failure rolls back every disk relabel too (same undo path), so a
//     non-nil error from MatchDiskProvider always means NO net change.
//  5. Trigger an immediate download-cycle convergence (parity with
//     Adopt/AddProvider) and return the refreshed SeriesDetailDTO (§16).
func (s *Service) MatchDiskProvider(ctx context.Context, seriesID, diskProviderID uuid.UUID, source string, mangaID int, scanlator string, importance int) (series.SeriesDetailDTO, error) {
	row, err := s.db.Series.Query().
		Where(entseries.IDEQ(seriesID)).
		WithCategory().
		Only(ctx)
	if ent.IsNotFound(err) {
		return series.SeriesDetailDTO{}, ErrSeriesNotFound
	}
	if err != nil {
		return series.SeriesDetailDTO{}, fmt.Errorf("library.MatchDiskProvider: load series %s: %w", seriesID, err)
	}

	diskSP, err := s.db.SeriesProvider.Query().
		Where(entseriesprovider.IDEQ(diskProviderID), entseriesprovider.SeriesID(seriesID)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return series.SeriesDetailDTO{}, ErrProviderNotInSeries
	}
	if err != nil {
		return series.SeriesDetailDTO{}, fmt.Errorf("library.MatchDiskProvider: load provider %s: %w", diskProviderID, err)
	}
	if diskSP.SuwayomiID != 0 {
		return series.SeriesDetailDTO{}, ErrNotADiskProvider
	}

	newSP, err := s.attachRealSource(ctx, seriesID, row.Title, source, mangaID, scanlator)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}

	done, err := s.relabelOverlap(ctx, row, diskSP, newSP)
	if err != nil {
		return series.SeriesDetailDTO{}, err
	}

	// The TARGET importance is applied only inside commitMatch's tx, atomically
	// with each chapter's satisfied_importance — see attachRealSource + commitMatch
	// for why elevating it any earlier would re-arm a re-download.
	if err := s.commitMatch(ctx, seriesID, diskProviderID, newSP.ID, importance, done); err != nil {
		s.rollbackRelabels(done)
		return series.SeriesDetailDTO{}, err
	}

	if s.trigger != nil {
		s.trigger()
	}

	return s.series.GetSeries(ctx, seriesID)
}

// attachRealSource ingests the chosen Suwayomi source (idempotent — a repeat
// call is a no-op per suwayomi.Ingest.AddSeries) and returns the resolved
// SeriesProvider row PARKED at importance 0 — deliberately NOT the owner's
// target importance.
//
// This is the crux of the no-redownload invariant. The background download
// dispatcher runs DetectUpgrades on its own ticker, UNSYNCHRONIZED with Match:
// there is no lock between them. If the new provider were elevated to the
// target (e.g. 5) here — before the disk relabel + the DB re-point — then during
// the relabel window (a full per-CBZ zip rebuild, seconds-to-minutes for a big
// series) the disk chapters would still be satisfied_importance=1 while the new
// source offered their keys at importance 5 → DetectUpgrades (strict 5 > 1)
// would flag every one upgrade_available and re-download the whole imported
// series. Parking at 0 keeps maxImportance(newSP)=0 <= satisfied_importance(1)
// for the entire window, so no upgrade can fire. The target is applied ONLY
// inside commitMatch's tx, atomically with the chapter re-point (importance ==
// satisfied thereafter). And on any rollback the provider stays parked at 0, so
// 0 <= 1 still holds — a failed Match never re-arms a re-download either.
//
// AddSeries leaves a freshly-created SeriesProvider at importance 0 (the schema
// default); the explicit SetImportance(0) additionally covers the rare case
// where the chosen source was ALREADY attached at a higher importance, so the
// disk window is safe there too.
func (s *Service) attachRealSource(ctx context.Context, seriesID uuid.UUID, seriesTitle, source string, mangaID int, scanlator string) (*ent.SeriesProvider, error) {
	if _, err := s.ingest.AddSeries(ctx, source, mangaID, seriesTitle, scanlator); err != nil {
		return nil, errors.Join(ErrSourceNotFound, err)
	}

	sp, err := s.db.SeriesProvider.Query().
		Where(entseriesprovider.SeriesID(seriesID), entseriesprovider.Provider(source), entseriesprovider.Scanlator(scanlator)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("library.MatchDiskProvider: load newly-attached provider: %w", err)
	}
	sp, err = sp.Update().SetImportance(0).Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("library.MatchDiskProvider: park importance: %w", err)
	}
	return sp, nil
}

// relabelOverlap is the disk-first phase: for every chapter the disk provider
// currently satisfies (state=downloaded) whose chapter_key the new source's
// feed also offers, it relabels the CBZ to the new source's identity. Any
// failure rolls back every relabel already performed in this call (reverse
// order) and returns the error with the DB left untouched.
func (s *Service) relabelOverlap(ctx context.Context, row *ent.Series, diskSP, newSP *ent.SeriesProvider) ([]relabeledChapter, error) {
	newFeed, err := s.db.ProviderChapter.Query().
		Where(entproviderchapter.SeriesProviderID(newSP.ID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("library.MatchDiskProvider: load new provider feed: %w", err)
	}
	pcByKey := make(map[string]*ent.ProviderChapter, len(newFeed))
	for _, pc := range newFeed {
		pcByKey[pc.ChapterKey] = pc
	}

	// Ordered by chapter number ascending so a mid-batch disk failure always
	// rolls back a deterministic, testable prefix of already-relabeled chapters
	// (lower-numbered chapters first, mirroring chapter.WantedChapters' order).
	diskChapters, err := s.db.Chapter.Query().
		Where(
			entchapter.SeriesID(row.ID),
			entchapter.StateEQ(entchapter.StateDownloaded),
			entchapter.SatisfiedByProviderIDEQ(diskSP.ID),
		).
		Order(entchapter.ByNumber()).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("library.MatchDiskProvider: load disk-satisfied chapters: %w", err)
	}

	maxChapter := maxChapterNumber(ctx, s.db, row.ID)
	categoryName := category.NameOf(row)
	diskLabel := strings.TrimSpace(series.ProviderLabel(diskSP))
	newLabel := strings.TrimSpace(series.ProviderLabel(newSP))

	var done []relabeledChapter
	for _, ch := range diskChapters {
		pc, ok := pcByKey[ch.ChapterKey]
		if !ok {
			// The new source doesn't offer this chapter — it stays satisfied by
			// the disk provider's watermark (importance 1); commitMatch clears
			// its dangling satisfied_by once the disk provider is deleted.
			continue
		}

		newMeta := disk.RenderMeta{
			Provider:            newSP.Provider,
			ProviderLabel:       newLabel,
			Scanlator:           newSP.Scanlator,
			Language:            newSP.Language,
			SeriesTitle:         row.Title,
			Category:            categoryName,
			Number:              pc.Number,
			MaxChapter:          maxChapter,
			ChapterName:         pc.Name,
			ChapterKey:          pc.ChapterKey,
			UploadDate:          pc.ProviderUploadDate,
			URL:                 pc.URL,
			Importance:          newSP.Importance,
			SeriesProviderTitle: newSP.Title,
		}
		oldMeta := disk.RenderMeta{
			Provider:            diskSP.Provider,
			ProviderLabel:       diskLabel,
			Scanlator:           diskSP.Scanlator,
			Language:            diskSP.Language,
			SeriesTitle:         row.Title,
			Category:            categoryName,
			Number:              ch.Number,
			MaxChapter:          maxChapter,
			ChapterKey:          ch.ChapterKey,
			Importance:          diskSP.Importance,
			SeriesProviderTitle: diskSP.Title,
		}

		newFilename, oldCI, rErr := disk.RelabelChapterFile(s.storage, newMeta, ch.Filename)
		if rErr != nil {
			s.rollbackRelabels(done)
			return nil, fmt.Errorf("library.MatchDiskProvider: relabel chapter %s: %w", ch.ID, rErr)
		}
		done = append(done, relabeledChapter{
			chapterID:   ch.ID,
			oldFilename: ch.Filename,
			newFilename: newFilename,
			oldCI:       oldCI,
			oldMeta:     oldMeta,
		})
	}

	return done, nil
}

// commitMatch is the all-or-nothing DB phase: elevate the new provider to the
// owner's target importance AND re-point every relabeled chapter onto it at that
// same importance — both in ONE tx, so no committed state ever has the provider
// outranking the chapters it satisfies (importance == satisfied_importance
// after commit ⇒ DetectUpgrades never fires). Also clears a dangling
// metadata_provider_id, clears satisfied_by (keeping the importance watermark)
// on any chapter the disk provider still satisfies that fell outside the
// overlap, then deletes the (now fully drained) disk provider.
//
// newImportance is the owner's target — the new provider was parked at 0 by
// attachRealSource and is raised to it here, atomically with the re-point.
func (s *Service) commitMatch(ctx context.Context, seriesID, diskProviderID, newProviderID uuid.UUID, newImportance int, done []relabeledChapter) error {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return fmt.Errorf("library.MatchDiskProvider: begin tx: %w", err)
	}

	// Elevate the new provider to the target importance in the SAME tx as the
	// chapter re-point below. Before this commit it was parked at 0 (<= the disk
	// chapters' satisfied_importance=1); after commit it equals each re-pointed
	// chapter's satisfied_importance — never strictly greater — so no committed
	// state can trip DetectUpgrades' strict-> gate.
	if err := tx.SeriesProvider.UpdateOneID(newProviderID).SetImportance(newImportance).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("library.MatchDiskProvider: set new provider importance: %w", err)
	}

	for _, r := range done {
		if err := tx.Chapter.UpdateOneID(r.chapterID).
			SetSatisfiedByProviderID(newProviderID).
			SetSatisfiedImportance(newImportance).
			SetFilename(r.newFilename).
			Exec(ctx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("library.MatchDiskProvider: re-point chapter %s: %w", r.chapterID, err)
		}
	}

	// Dangling-pointer guard: mirrors series.RemoveProvider — if the series'
	// metadata_provider_id currently points at the disk provider being
	// deleted, clear it (no-op if absent or pointing elsewhere).
	if err := tx.Series.Update().
		Where(entseries.IDEQ(seriesID), entseries.MetadataProviderIDEQ(diskProviderID)).
		ClearMetadataProviderID().
		Exec(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("library.MatchDiskProvider: clear dangling metadata_provider_id: %w", err)
	}

	if err := deleteDrainedDiskProvider(ctx, tx, diskProviderID); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("library.MatchDiskProvider: commit tx: %w", err)
	}
	return nil
}

// deleteDrainedDiskProvider removes the now-fully-drained disk provider inside
// the caller's tx, FK-safe (mirrors series.removeProviderInTx MINUS the
// satisfied_by clearing — commitMatch already re-pointed the overlap): (1)
// clear satisfied_by on any leftover chapter the new source did not cover (keep
// the satisfied_importance watermark) so it never dangles once the row is gone,
// then delete (2) the disk provider's ProviderChapter feed (always empty for a
// disk-origin provider), (3) its SuwayomiSyncState, (4) the SeriesProvider row.
func deleteDrainedDiskProvider(ctx context.Context, tx *ent.Tx, diskProviderID uuid.UUID) error {
	if err := tx.Chapter.Update().
		Where(entchapter.SatisfiedByProviderIDEQ(diskProviderID)).
		ClearSatisfiedBy().
		Exec(ctx); err != nil {
		return fmt.Errorf("library.MatchDiskProvider: clear leftover satisfied_by: %w", err)
	}
	if _, err := tx.ProviderChapter.Delete().
		Where(entproviderchapter.SeriesProviderID(diskProviderID)).
		Exec(ctx); err != nil {
		return fmt.Errorf("library.MatchDiskProvider: delete disk provider chapters: %w", err)
	}
	if _, err := tx.SuwayomiSyncState.Delete().
		Where(entsuwayomisyncstate.SeriesProviderID(diskProviderID)).
		Exec(ctx); err != nil {
		return fmt.Errorf("library.MatchDiskProvider: delete disk provider sync state: %w", err)
	}
	if err := tx.SeriesProvider.DeleteOneID(diskProviderID).Exec(ctx); err != nil {
		return fmt.Errorf("library.MatchDiskProvider: delete disk provider: %w", err)
	}
	return nil
}

// rollbackRelabels undoes every relabel in done, in reverse order, via
// disk.UndoRelabelChapterFile. It is best-effort: an undo failure is logged
// (not swallowed silently) but does not stop unwinding the rest — a single
// stuck file must not strand every other chapter mid-rollback.
func (s *Service) rollbackRelabels(done []relabeledChapter) {
	for i := len(done) - 1; i >= 0; i-- {
		r := done[i]
		if err := disk.UndoRelabelChapterFile(s.storage, r.oldMeta, r.newFilename, r.oldFilename, r.oldCI); err != nil {
			logRollbackFailure(r.chapterID, err)
		}
	}
}

// maxChapterNumber returns the highest known Chapter.Number for a series, or
// nil if none have a parsed number. It feeds RenderMeta.MaxChapter so a
// relabeled filename zero-pads consistently with the rest of the series —
// mirrors download's maxChapterNumber aggregate pattern, but reads
// Chapter.Number directly (already the resolved display value set at ingest;
// this package has no import path to internal/download).
func maxChapterNumber(ctx context.Context, client *ent.Client, seriesID uuid.UUID) *float64 {
	var result []struct {
		Max *float64 `json:"max"`
	}
	err := client.Chapter.Query().
		Where(entchapter.SeriesID(seriesID), entchapter.NumberNotNil()).
		Aggregate(ent.Max(entchapter.FieldNumber)).
		Scan(ctx, &result)
	if err != nil || len(result) == 0 || result[0].Max == nil {
		return nil
	}
	return result[0].Max
}

// logRollbackFailure is a tiny seam so rollbackRelabels' best-effort logging
// is exercised the same way everywhere a disk undo fails.
func logRollbackFailure(chapterID uuid.UUID, err error) {
	slog.Error("library.MatchDiskProvider: rollback of a relabeled chapter failed — file may be left under its NEW name/identity",
		"chapter_id", chapterID,
		"err", err,
	)
}
