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

// PushProgress evaluates seriesID's EVERY TrackBinding against localFurthest
// (the local reading-progress trigger: a reader-marked chapter, phase-4 spec
// §2 trigger (a)) and pushes to whichever bindings the never-regress rule
// (sync.NextPush) says need it. It is the reading-triggered + manual-trigger
// entry point (both the series.ProgressPusher reader hook and any future
// manual "push now" affordance call this the same way).
//
// GATING: when an AutoUpdateTracker was attached and reports the toggle
// off, this is a silent no-op (spec §2: reading-triggered sync is gated by
// auto_update_track) — a nil AutoUpdateTracker means "always enabled".
// UNPARSEABLE FILTER: localFurthest is passed through sync.SyncableNumbers
// first — the chapter normaliser's -1 sentinel (or any other negative/NaN
// value) is filtered out of ALL sync (spec §2(c)), so a chapter whose
// number could not be parsed can never corrupt a tracker's progress.
//
// PER-BINDING ISOLATION: one binding's push failure is logged + enqueued to
// the durable retry queue (never-lose-progress) and does NOT abort the
// batch — every other binding still gets its own attempt. The aggregated
// per-binding errors (if any) are returned via errors.Join so a caller that
// wants to know CAN, but the reader hook (best-effort, §16 sanctioned
// exception) is expected to discard it.
func (s *Service) PushProgress(ctx context.Context, seriesID uuid.UUID, localFurthest float64) error {
	if s.autoUpdate != nil && !s.autoUpdate.AutoUpdateTrack(ctx) {
		return nil
	}
	if len(kernel.SyncableNumbers([]float64{localFurthest})) == 0 {
		return nil // unparseable/negative chapter number — never sync.
	}

	bindings, err := s.client.TrackBinding.Query().Where(enttrackbinding.SeriesID(seriesID)).All(ctx)
	if err != nil {
		return fmt.Errorf("syncsvc: PushProgress: load bindings for series %s: %w", seriesID, err)
	}

	var errs []error
	for _, b := range bindings {
		if pushErr := s.pushOne(ctx, b, localFurthest); pushErr != nil {
			slog.WarnContext(ctx, "syncsvc: push failed, enqueueing for retry",
				"track_binding_id", b.ID, "series_id", seriesID, "err", pushErr)
			if enqErr := s.retryQueue.Enqueue(ctx, b.ID, localFurthest); enqErr != nil {
				slog.WarnContext(ctx, "syncsvc: enqueue after push failure also failed",
					"track_binding_id", b.ID, "err", enqErr)
			}
			errs = append(errs, pushErr)
		}
	}
	return errors.Join(errs...)
}

// pushOne applies the never-regress decision (sync.NextPush) to ONE binding
// and, when a push is warranted, calls UpdateEntry and persists the result
// locally + to the sidecar. It returns nil both when NextPush declines
// (already up to date — nothing to do, not an error) and on a successful
// push; it returns a non-nil error ONLY on a genuine push failure.
//
// 🔴 pushOne itself NEVER enqueues to the retry queue on failure — that is
// the CALLER's decision. PushProgress/SyncNow (fresh triggers) do enqueue on
// failure; Push (the retry.Pusher implementation, pusher.go) does NOT — that
// row's own backoff/attempts bookkeeping is already owned by
// retry.Queue.RunOnce, and a second Enqueue here would reset it to a fresh
// budget, defeating the hard attempt cap (spec §3).
func (s *Service) pushOne(ctx context.Context, binding *ent.TrackBinding, localFurthest float64) error {
	push, shouldPush := kernel.NextPush(localFurthest, binding.LastChapterRead)
	if !shouldPush {
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

	// Truncate to whatever the tracker's wire field actually stores (spec §2:
	// "truncate to int for integer-count trackers" — every tracker in this
	// registry today is integer-count; see sync.TruncateForInteger's doc
	// comment). Persisting the TRUNCATED value locally (not the raw
	// fractional push) keeps TrackBinding.last_chapter_read equal to what the
	// remote entry actually now holds.
	truncated := float64(kernel.TruncateForInteger(push))

	now := time.Now().UTC()
	entry := tracker.TrackEntry{
		RemoteID:  binding.RemoteID,
		LibraryID: binding.LibraryID,
		Progress:  truncated,
	}
	if binding.StartDate == nil {
		// First-ever progress on this binding: stamp (and push) a start date.
		entry.StartDate = &now
	}
	if binding.TotalChapters > 0 && kernel.ShouldAutoComplete(truncated, float64(binding.TotalChapters)) {
		entry.FinishDate = &now
		if status, ok := completedStatus(binding.TrackerID); ok {
			entry.Status = status
		}
	}

	if _, err := t.UpdateEntry(ctx, token, entry); err != nil {
		return fmt.Errorf("syncsvc: push binding %s to %s: %w", binding.ID, t.Key(), err)
	}

	upd := binding.Update().SetLastChapterRead(truncated)
	if entry.StartDate != nil {
		upd = upd.SetStartDate(*entry.StartDate)
	}
	if entry.FinishDate != nil {
		upd = upd.SetFinishDate(*entry.FinishDate)
	}
	if entry.Status != "" {
		upd = upd.SetStatus(entry.Status)
	}
	updated, err := upd.Save(ctx)
	if err != nil {
		return fmt.Errorf("syncsvc: persist push result for binding %s: %w", binding.ID, err)
	}

	s.syncSidecar(ctx, updated.SeriesID)
	return nil
}
