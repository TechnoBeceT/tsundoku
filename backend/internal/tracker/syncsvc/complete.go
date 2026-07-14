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

// completeOne force-sets ONE binding to its tracker's native completed status
// (and, when the tracker reported a total, advances progress to it), pushing
// the change to the tracker's own account and persisting it locally. A binding
// already at its completed status with progress already at/beyond its total is
// a no-op (no remote call). A tracker absent from propagatedCompletedStatus is
// skipped (nothing sensible to set). Progress is only ever ADVANCED, never
// regressed (target is max(current, total)).
func (s *Service) completeOne(ctx context.Context, binding *ent.TrackBinding) error {
	status, ok := propagatedCompletedStatus(binding.TrackerID)
	if !ok {
		return nil
	}
	truncated := completionProgress(binding)
	if binding.Status == status && binding.LastChapterRead >= truncated {
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

// completionProgress is the progress a completed binding should hold: its
// total when the tracker reports one AND it is ahead of the current read
// (completion fills progress to the end), else the current read — never a
// regress. Truncated to the tracker's integer wire field (mirrors pushOne).
func completionProgress(binding *ent.TrackBinding) float64 {
	target := binding.LastChapterRead
	if binding.TotalChapters > 0 && float64(binding.TotalChapters) > target {
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
