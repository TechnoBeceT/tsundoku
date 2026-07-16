package syncsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	enttrackbinding "github.com/technobecet/tsundoku/internal/ent/trackbinding"
	"github.com/technobecet/tsundoku/internal/tracker"
	kernel "github.com/technobecet/tsundoku/internal/tracker/sync"
)

// CompleteSeries force-sets status=completed on EVERY one of seriesID's
// TrackBinding entries — cross-tracker completion PROPAGATION (QCAT-243). It
// fires when ONE bound tracker reaches "completed" (the owner sets it on the
// edit sheet — see UpdateTrack — or a tracker auto-completes on reaching its
// OWN reported total — see pushOne/PushProgress), fanning that terminal status
// out to the series' OTHER bound trackers.
//
// WHY a fan-out rather than per-tracker inference: trackers disagree on
// whether they report a total chapter count. A tracker that reports none
// (MangaUpdates) can NEVER auto-complete on "reached total", so without this
// propagation it stays stuck at "reading" while its siblings (e.g. MAL, which
// reports a total) show "completed". "completed" must therefore travel as an
// explicit STATUS, not be inferred independently per tracker.
//
// For a tracker that DOES report a total, completion also advances progress to
// that total; a tracker with no known total keeps its current progress and
// only its status moves. completed is a TERMINAL status, so setting it is
// never a progress REGRESS (the never-regress invariant sync.NextPush guards
// still holds) — this reuses the SAME per-binding-isolation fan-out shape as
// SetSeriesProgress (QCAT-242) rather than adding a new never-regress bypass.
//
// PER-BINDING ISOLATION (mirrors SetSeriesProgress): one binding's remote
// failure is logged and does NOT abort the rest — every other binding still
// completes. Aggregated per-binding errors are returned via errors.Join.
//
// This is completion propagation ONLY: it never UN-completes a binding (the
// reverse — reopening on a downward reset — lives in SetSeriesProgress).
func (s *Service) CompleteSeries(ctx context.Context, seriesID uuid.UUID) error {
	bindings, err := s.client.TrackBinding.Query().Where(enttrackbinding.SeriesID(seriesID)).All(ctx)
	if err != nil {
		return fmt.Errorf("syncsvc: CompleteSeries: load bindings for series %s: %w", seriesID, err)
	}

	var errs []error
	for _, b := range bindings {
		if err := s.completeOne(ctx, b); err != nil {
			slog.WarnContext(ctx, "syncsvc: complete propagation failed for one binding, continuing with the rest",
				"track_binding_id", b.ID, "series_id", seriesID, "err", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// seriesReadCompleted reports whether ANY of a series' bindings has reached
// READ-completion — either it sits at its tracker's OWN native completed
// status (a completed status the owner set on the tracker, or a prior
// auto-complete, adopted onto the row by a pull — isPropagatedCompletedStatus
// covers all four trackers, MangaUpdates' "complete" list label included), OR
// it has read to/past its OWN reported total.
//
// 🔴 READ-completion means the user has READ every chapter. It is a DIFFERENT
// axis from the local Series.completed library flag (which means
// release/publication-complete + stop-monitoring), and this function
// DELIBERATELY never reads that flag — cross-tracker completion keys off
// read-completion ONLY. Completed is terminal, so an OR across bindings can
// never wrongly regress anyone: the CompleteSeries fan-out this gates only
// ever advances a binding to completed, never un-completes one.
func seriesReadCompleted(bindings []*ent.TrackBinding) bool {
	for _, b := range bindings {
		if isPropagatedCompletedStatus(b.TrackerID, b.Status) {
			return true
		}
		if b.TotalChapters > 0 && b.LastChapterRead >= float64(b.TotalChapters) {
			return true
		}
	}
	return false
}

// completeOne force-sets ONE binding to its tracker's native completed status
// and lands its progress at completionProgress (the tracker's OWN total when
// it reports one, else the current read), pushing the change to the tracker's
// own account and persisting it locally. A binding already at its completed
// status with progress EXACTLY at completionProgress is a no-op (no remote
// call). A tracker absent from propagatedCompletedStatus is skipped (nothing
// sensible to set).
func (s *Service) completeOne(ctx context.Context, binding *ent.TrackBinding) error {
	status, ok := propagatedCompletedStatus(binding.TrackerID)
	if !ok {
		return nil
	}
	truncated := completionProgress(binding)
	// == (not >=): completionProgress CLAMPS to the tracker's own total when
	// known, so an already-completed binding that overshot its total (e.g. MAL
	// stored read 269 against its 268-chapter catalog — read > total) is NOT a
	// no-op here; it must be corrected down to exactly 268. See
	// completionProgress.
	if binding.Status == status && binding.LastChapterRead == truncated {
		return nil
	}

	t, ok := s.registry.ByID(binding.TrackerID)
	if !ok {
		return ErrTrackerNotFound
	}
	token, err := s.accountToken(ctx, binding.TrackerID)
	if err != nil {
		return err
	}
	return s.pushAndPersistCompletion(ctx, t, token, binding, status, truncated)
}

// capCompletedToRemoteTotal caps progress at the tracker's OWN reported total
// when the remote entry is COMPLETED and reports a total (>0) — SyncNow's
// stored/pushed value for a completed with-total binding. It exists so the
// pull-side up-push (syncOneBinding) and the completion clamp-down
// (completeOne/completionProgress) AGREE on a SINGLE resting target — the
// tracker's own total — instead of oscillating: without it, seriesLocalFurthest
// (which can exceed one tracker's catalog when a sibling or the local library
// read further, e.g. local 269 vs MAL's 268-chapter total) dragged the stored
// value to 269 every sync, pushBack pushed 269, then completeOne clamped back
// to 268 — two redundant remote writes forever. Capping here makes pushBack AND
// completeOne no-ops once settled.
//
// It only ever caps DOWNWARD (progress > total) and ONLY for a COMPLETED
// binding — an ongoing/reading series is left untouched (its tracker's total
// may simply lag the real release count, and capping a legitimate higher read
// down to a stale total would lose progress). A tracker that reports no total
// (MangaUpdates, total 0) or is not at its completed status is returned
// unchanged (isCompletedStatus is false for MangaUpdates regardless).
func capCompletedToRemoteTotal(trackerID int, progress float64, remote *tracker.TrackEntry) float64 {
	if remote.TotalChapters > 0 && isCompletedStatus(trackerID, remote.Status) && progress > float64(remote.TotalChapters) {
		return float64(remote.TotalChapters)
	}
	return progress
}

// completionProgress is the progress a completed binding should hold: EXACTLY
// the tracker's OWN reported total when it reports one (>0), else the current
// read (unknown total — keep whatever the owner has). When the total is known
// this CLAMPS both up AND down to that total — the read a completed binding
// stores must be that tracker's own catalog count, not a cross-tracker
// maximum: a tracker whose catalog lists 268 chapters must store 268/268, even
// when a sibling tracker (or the local library) read to 269. This fixes the
// observed MAL "269/268" (read > total) desync — read=max-across-sources
// (269) vs total=this-tracker's-catalog (268) — by landing MAL at 268/268.
// Truncated to the tracker's integer wire field (mirrors pushOne).
func completionProgress(binding *ent.TrackBinding) float64 {
	target := binding.LastChapterRead
	if binding.TotalChapters > 0 {
		target = float64(binding.TotalChapters)
	}
	return float64(kernel.TruncateForInteger(target))
}

// pushAndPersistCompletion writes the completed status + progress (+ a finish
// date, on a binding that has none yet) to the tracker's own account, then
// persists the same fields locally and syncs the sidecar — completeOne's
// remote+local tail, extracted to keep completeOne under the fleet's
// per-function complexity budget.
func (s *Service) pushAndPersistCompletion(ctx context.Context, t tracker.Tracker, token string, binding *ent.TrackBinding, status string, truncated float64) error {
	now := time.Now().UTC()
	setFinish := binding.FinishDate == nil

	entry := baseEntryFromBinding(binding)
	entry.Progress = truncated
	entry.Status = status
	if setFinish {
		entry.FinishDate = &now
	}

	if _, err := t.UpdateEntry(ctx, token, entry); err != nil {
		s.markExpiredOnTokenFailure(ctx, binding.TrackerID, err)
		return tracker.WrapUpstream(t.Key(), fmt.Errorf("syncsvc: CompleteSeries: complete binding %s on %s: %w", binding.ID, t.Key(), err))
	}

	upd := binding.Update().SetStatus(status).SetLastChapterRead(truncated)
	if setFinish {
		upd = upd.SetFinishDate(now)
	}
	updated, err := upd.Save(ctx)
	if err != nil {
		return fmt.Errorf("syncsvc: CompleteSeries: persist binding %s: %w", binding.ID, err)
	}

	s.syncSidecar(ctx, updated.SeriesID)
	return nil
}
