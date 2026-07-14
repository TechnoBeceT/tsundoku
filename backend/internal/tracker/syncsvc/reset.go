package syncsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	enttrackbinding "github.com/technobecet/tsundoku/internal/ent/trackbinding"
	"github.com/technobecet/tsundoku/internal/tracker"
	kernel "github.com/technobecet/tsundoku/internal/tracker/sync"
)

// SetSeriesProgress force-sets EVERY one of seriesID's TrackBinding entries
// to target — the tracker half of the QCAT-242 "set reading progress to N"
// action (series.Service.SetReadingProgress is the local-chapter half; the
// HTTP handler orchestrates the two together, never coupling them here).
//
// 🔴 NEVER-REGRESS DELIBERATELY BYPASSED: unlike PushProgress/pushOne (which
// gate every write through sync.NextPush, the ACCIDENTAL-regression guard),
// this is the ONE sanctioned path that may LOWER a tracker's progress —
// QCAT-242's explicit owner reset is a different intent than a stale/
// straggling reading-push and must not be silently declined by NextPush.
// forceSetOne therefore calls UpdateEntry directly, with no NextPush check
// at all.
//
// PER-BINDING ISOLATION (mirrors PushProgress): one binding's failure
// (tracker unreachable, tracker rejected the write, the account has since
// been disconnected) is logged and does NOT abort the rest of the series'
// bindings — every other binding still gets its own attempt. The aggregated
// per-binding errors are returned via errors.Join, unwrapped (never
// enqueued to the retry queue — this is a synchronous owner action the
// caller is expected to see fail, not a background push to retry later).
func (s *Service) SetSeriesProgress(ctx context.Context, seriesID uuid.UUID, target float64) error {
	bindings, err := s.client.TrackBinding.Query().Where(enttrackbinding.SeriesID(seriesID)).All(ctx)
	if err != nil {
		return fmt.Errorf("syncsvc: SetSeriesProgress: load bindings for series %s: %w", seriesID, err)
	}

	var errs []error
	for _, b := range bindings {
		if err := s.forceSetOne(ctx, b, target); err != nil {
			slog.WarnContext(ctx, "syncsvc: force-set progress failed for one binding, continuing with the rest",
				"track_binding_id", b.ID, "series_id", seriesID, "err", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// forceSetOne force-sets ONE binding's tracker progress to target, bypassing
// sync.NextPush entirely (see SetSeriesProgress's doc comment). Truncates to
// whatever the tracker's wire field actually stores (mirrors pushOne — every
// tracker in this registry today is integer-count).
//
// REOPEN ON REGRESSION (QCAT-242): when target lowers the binding's progress
// below its CURRENT last_chapter_read AND the binding's stored status is
// exactly that tracker's own completedStatus, the entry (and the persisted
// binding) is ALSO moved back to the tracker's native "reading/current"
// status via readingStatus — a completed entry the owner just re-opened by
// rewinding progress must not keep reading "Completed". A tracker with no
// native status concept (MangaUpdates) or a binding that wasn't actually
// completed is left with its status untouched; only progress moves.
func (s *Service) forceSetOne(ctx context.Context, binding *ent.TrackBinding, target float64) error {
	t, ok := s.registry.ByID(binding.TrackerID)
	if !ok {
		return ErrTrackerNotFound
	}
	token, err := s.accountToken(ctx, binding.TrackerID)
	if err != nil {
		return err
	}

	truncated := float64(kernel.TruncateForInteger(target))
	entry := baseEntryFromBinding(binding)
	entry.Progress = truncated

	reopen := target < binding.LastChapterRead && isCompletedStatus(binding.TrackerID, binding.Status)
	if reopen {
		if status, ok := readingStatus(binding.TrackerID); ok {
			entry.Status = status
		}
	}

	if _, err := t.UpdateEntry(ctx, token, entry); err != nil {
		s.markExpiredOnTokenFailure(ctx, binding.TrackerID, err)
		return tracker.WrapUpstream(t.Key(), fmt.Errorf("syncsvc: SetSeriesProgress: force-set binding %s on %s: %w", binding.ID, t.Key(), err))
	}

	upd := binding.Update().SetLastChapterRead(truncated)
	if reopen && entry.Status != "" {
		upd = upd.SetStatus(entry.Status)
	}
	updated, err := upd.Save(ctx)
	if err != nil {
		return fmt.Errorf("syncsvc: SetSeriesProgress: persist binding %s: %w", binding.ID, err)
	}

	s.syncSidecar(ctx, updated.SeriesID)
	return nil
}
