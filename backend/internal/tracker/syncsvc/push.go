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
	anyCompleted := false
	for _, b := range bindings {
		completed, pushErr := s.pushOne(ctx, b, localFurthest)
		if pushErr != nil {
			s.enqueueFailedPush(ctx, b, localFurthest, pushErr)
			errs = append(errs, pushErr)
			continue
		}
		anyCompleted = anyCompleted || completed
	}

	// BUG-4 (QCAT-243): if a reading push auto-completed any binding (it
	// reached that tracker's OWN reported total), propagate the terminal
	// status to the series' OTHER trackers — including any that report no
	// total and so could never auto-complete themselves.
	if anyCompleted {
		if err := s.CompleteSeries(ctx, seriesID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// enqueueFailedPush logs a per-binding push failure and enqueues it on the
// durable retry queue (never-lose-progress) — extracted from PushProgress's
// loop so that method stays under the fleet's per-function complexity budget.
// An enqueue that ALSO fails is logged, not returned: the original push error
// is what the caller already records.
func (s *Service) enqueueFailedPush(ctx context.Context, b *ent.TrackBinding, localFurthest float64, pushErr error) {
	slog.WarnContext(ctx, "syncsvc: push failed, enqueueing for retry",
		"track_binding_id", b.ID, "series_id", b.SeriesID, "err", pushErr)
	if enqErr := s.retryQueue.Enqueue(ctx, b.ID, localFurthest); enqErr != nil {
		slog.WarnContext(ctx, "syncsvc: enqueue after push failure also failed",
			"track_binding_id", b.ID, "err", enqErr)
	}
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
//
// It returns completed=true when this push AUTO-COMPLETED the binding (it
// reached the tracker's OWN reported total) — the signal PushProgress/Push use
// to fan the terminal status out to the series' other trackers (BUG-4). A
// declined push (already up to date) and a plain non-completing push both
// return completed=false.
func (s *Service) pushOne(ctx context.Context, binding *ent.TrackBinding, localFurthest float64) (bool, error) {
	push, shouldPush := kernel.NextPush(localFurthest, binding.LastChapterRead)
	if !shouldPush {
		return false, nil
	}

	t, ok := s.registry.ByID(binding.TrackerID)
	if !ok {
		return false, ErrTrackerNotFound
	}
	token, err := s.accountToken(ctx, binding.TrackerID)
	if err != nil {
		return false, err
	}

	// Truncate to whatever the tracker's wire field actually stores (spec §2:
	// "truncate to int for integer-count trackers" — every tracker in this
	// registry today is integer-count; see sync.TruncateForInteger's doc
	// comment). Persisting the TRUNCATED value locally (not the raw
	// fractional push) keeps TrackBinding.last_chapter_read equal to what the
	// remote entry actually now holds.
	truncated := float64(kernel.TruncateForInteger(push))
	entry := buildPushEntry(binding, truncated, time.Now().UTC())

	if _, err := t.UpdateEntry(ctx, token, entry); err != nil {
		s.markExpiredOnTokenFailure(ctx, binding.TrackerID, err)
		return false, fmt.Errorf("syncsvc: push binding %s to %s: %w", binding.ID, t.Key(), err)
	}

	upd := applyPushEntryToUpdate(binding.Update().SetLastChapterRead(truncated), entry)
	updated, err := upd.Save(ctx)
	if err != nil {
		return false, fmt.Errorf("syncsvc: persist push result for binding %s: %w", binding.ID, err)
	}

	s.syncSidecar(ctx, updated.SeriesID)
	return autoCompletes(binding, truncated), nil
}

// autoCompletes reports whether pushing truncated onto binding reaches the
// tracker's OWN reported total (a non-zero total the read count has met) — the
// SINGLE condition shared by buildPushEntry (which sets the completed status +
// finish date) and pushOne (which reports it up for cross-tracker
// propagation), so the "auto-complete" rule lives in exactly one place.
func autoCompletes(binding *ent.TrackBinding, truncated float64) bool {
	return binding.TotalChapters > 0 && kernel.ShouldAutoComplete(truncated, float64(binding.TotalChapters))
}

// buildPushEntry constructs the TrackEntry pushOne sends to the tracker for
// binding, given the already-truncated progress value and "now". It seeds
// EVERY field from binding's own currently-persisted state
// (baseEntryFromBinding — see that helper's doc comment for why: every
// concrete Tracker client full-field-writes, so a sparse entry would
// clobber the remote's score/privacy/status) and then makes the two
// decisions pushOne needs on top of that seed: stamp a start date on the
// binding's first-ever push, and (spec §2) auto-complete when the tracker
// reported a non-zero total that truncated has now reached — only then does
// it look up the tracker's own native completed-status string via
// completedStatus, overriding the carried-through binding.Status.
func buildPushEntry(binding *ent.TrackBinding, truncated float64, now time.Time) tracker.TrackEntry {
	entry := baseEntryFromBinding(binding)
	entry.Progress = truncated
	if binding.StartDate == nil {
		// First-ever progress on this binding: stamp (and push) a start date.
		entry.StartDate = &now
	}
	if autoCompletes(binding, truncated) {
		entry.FinishDate = &now
		if status, ok := completedStatus(binding.TrackerID); ok {
			entry.Status = status
		}
	}
	return entry
}

// applyPushEntryToUpdate mirrors entry's StartDate/FinishDate/Status onto
// upd (the local persistence builder) so the just-pushed remote entry and
// the locally persisted binding can never drift on which optional fields
// changed.
func applyPushEntryToUpdate(upd *ent.TrackBindingUpdateOne, entry tracker.TrackEntry) *ent.TrackBindingUpdateOne {
	if entry.StartDate != nil {
		upd = upd.SetStartDate(*entry.StartDate)
	}
	if entry.FinishDate != nil {
		upd = upd.SetFinishDate(*entry.FinishDate)
	}
	if entry.Status != "" {
		upd = upd.SetStatus(entry.Status)
	}
	return upd
}
