package syncsvc

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	enttrackbinding "github.com/technobecet/tsundoku/internal/ent/trackbinding"
	"github.com/technobecet/tsundoku/internal/tracker"
	kernel "github.com/technobecet/tsundoku/internal/tracker/sync"
)

// SyncNow pulls EVERY one of seriesID's TrackBinding entries from its
// tracker's own account (GetEntry) and converges local↔remote per the
// umbrella spec §6 "conflict = MAX wins BOTH directions" rule
// (sync.Converge): whichever side (local or remote) is behind adopts the
// higher value. When local was strictly ahead, the converged value is ALSO
// pushed back to the remote (reusing sync.NextPush's own decision) so both
// sides genuinely end up in agreement, not just the local row.
//
// One binding's failure (GetEntry/UpdateEntry error, unregistered/
// disconnected tracker) is logged and that binding's PRE-SYNC row is kept
// in the result unchanged — it never aborts syncing the series' other
// bindings. A hard error is returned only if the initial binding-set query
// itself fails.
func (s *Service) SyncNow(ctx context.Context, seriesID uuid.UUID) ([]*ent.TrackBinding, error) {
	bindings, err := s.client.TrackBinding.Query().Where(enttrackbinding.SeriesID(seriesID)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("syncsvc: SyncNow: load bindings for series %s: %w", seriesID, err)
	}

	out := make([]*ent.TrackBinding, 0, len(bindings))
	for _, b := range bindings {
		updated, syncErr := s.syncOneBinding(ctx, b)
		if syncErr != nil {
			slog.WarnContext(ctx, "syncsvc: SyncNow: binding sync failed",
				"track_binding_id", b.ID, "series_id", seriesID, "err", syncErr)
			out = append(out, b)
			continue
		}
		out = append(out, updated)
	}
	return out, nil
}

// syncOneBinding pulls b's remote entry and converges it with the local row.
// A nil GetEntry result (the manga has since vanished from the account's
// list — a valid empty state, not an error, mirroring bind.Service.
// FetchTrack) leaves b unchanged: the owner can explicitly Unbind if the
// drift is intentional.
func (s *Service) syncOneBinding(ctx context.Context, b *ent.TrackBinding) (*ent.TrackBinding, error) {
	t, ok := s.registry.ByID(b.TrackerID)
	if !ok {
		return nil, ErrTrackerNotFound
	}
	token, err := s.accountToken(ctx, b.TrackerID)
	if err != nil {
		return nil, err
	}

	remote, err := t.GetEntry(ctx, token, b.RemoteID)
	if err != nil {
		return nil, fmt.Errorf("syncsvc: fetch remote entry from %s for binding %s: %w", t.Key(), b.ID, err)
	}
	if remote == nil {
		return b, nil
	}

	converged := kernel.Converge(b.LastChapterRead, remote.Progress)
	truncated := float64(kernel.TruncateForInteger(converged))

	upd := b.Update().
		SetLastChapterRead(truncated).
		SetTotalChapters(remote.TotalChapters).
		SetScore(remote.Score).
		SetPrivate(remote.Private)
	if remote.Status != "" {
		upd = upd.SetStatus(remote.Status)
	}
	if remote.LibraryID != "" {
		upd = upd.SetLibraryID(remote.LibraryID)
	}
	if remote.StartDate != nil {
		upd = upd.SetStartDate(*remote.StartDate)
	}
	if remote.FinishDate != nil {
		upd = upd.SetFinishDate(*remote.FinishDate)
	}

	// Local was strictly ahead of the just-fetched remote: push the converged
	// value so the remote side genuinely catches up too (Converge only picks
	// the TARGET both sides move to; NextPush is the reused decision for
	// whether THIS side still needs to send it — see Converge's own doc
	// comment). A push failure here does not fail the whole sync — the local
	// row's convergence is still correct and durable; the retry queue carries
	// the outstanding remote push.
	if push, shouldPush := kernel.NextPush(truncated, remote.Progress); shouldPush {
		entry := tracker.TrackEntry{RemoteID: b.RemoteID, LibraryID: b.LibraryID, Progress: push}
		if _, pushErr := t.UpdateEntry(ctx, token, entry); pushErr != nil {
			slog.WarnContext(ctx, "syncsvc: SyncNow: push-back failed, enqueueing for retry",
				"track_binding_id", b.ID, "err", pushErr)
			if enqErr := s.retryQueue.Enqueue(ctx, b.ID, push); enqErr != nil {
				slog.WarnContext(ctx, "syncsvc: SyncNow: enqueue after push-back failure also failed",
					"track_binding_id", b.ID, "err", enqErr)
			}
		}
	}

	updated, err := upd.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("syncsvc: persist sync result for binding %s: %w", b.ID, err)
	}
	s.syncSidecar(ctx, updated.SeriesID)
	return updated, nil
}
