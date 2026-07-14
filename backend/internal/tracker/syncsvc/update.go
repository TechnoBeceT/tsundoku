package syncsvc

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	enttrackbinding "github.com/technobecet/tsundoku/internal/ent/trackbinding"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// UpdatePatch is the owner's manual tracking-sheet edit — every field is a
// pointer so a nil field means "leave unchanged", the same partial-PATCH
// shape used across this codebase (e.g. suwayomi's SuwayomiSettingsPatch).
// The HTTP handler's validate.go requires at least one non-nil field before
// calling UpdateTrack.
type UpdatePatch struct {
	Status          *string
	LastChapterRead *float64
	Score           *float64
	StartDate       *time.Time
	FinishDate      *time.Time
	Private         *bool
}

// UpdateTrack applies the owner's manual tracking-sheet edit (phase-4 spec
// §2 trigger (c)) to recordID's binding: it pushes EVERY patched field to
// the tracker's own account in one UpdateEntry call, then persists the same
// fields locally and mirrors the series' binding set to its sidecar.
//
// Unlike PushProgress/SyncNow (best-effort, reading/background-triggered),
// a failure here IS returned to the caller — the owner explicitly asked for
// this edit, so a silent drop would violate §16 (no silent operations).
//
// Returns ErrBindingNotFound for an unknown recordID, ErrTrackerNotFound /
// ErrTrackerNotConnected for a binding whose tracker has since been
// unregistered/disconnected. Any other error is a genuine upstream/
// persistence failure.
func (s *Service) UpdateTrack(ctx context.Context, recordID uuid.UUID, patch UpdatePatch) (*ent.TrackBinding, error) {
	binding, err := s.client.TrackBinding.Query().Where(enttrackbinding.IDEQ(recordID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrBindingNotFound
		}
		return nil, fmt.Errorf("syncsvc: UpdateTrack: load binding %s: %w", recordID, err)
	}

	t, ok := s.registry.ByID(binding.TrackerID)
	if !ok {
		return nil, ErrTrackerNotFound
	}
	token, err := s.accountToken(ctx, binding.TrackerID)
	if err != nil {
		return nil, err
	}

	entry := tracker.TrackEntry{
		RemoteID:      binding.RemoteID,
		LibraryID:     binding.LibraryID,
		Status:        binding.Status,
		Score:         binding.Score,
		Progress:      binding.LastChapterRead,
		TotalChapters: binding.TotalChapters,
		StartDate:     binding.StartDate,
		FinishDate:    binding.FinishDate,
		Private:       binding.Private,
	}
	upd := applyUpdatePatch(&entry, binding.Update(), patch)

	if _, err := t.UpdateEntry(ctx, token, entry); err != nil {
		return nil, fmt.Errorf("syncsvc: UpdateTrack: push edit to %s for binding %s: %w", t.Key(), recordID, err)
	}

	updated, err := upd.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("syncsvc: UpdateTrack: persist binding %s: %w", recordID, err)
	}

	s.syncSidecar(ctx, updated.SeriesID)
	return updated, nil
}

// applyUpdatePatch applies every non-nil field of patch to BOTH entry (the
// TrackEntry UpdateTrack pushes to the tracker) and upd (the local
// persistence builder), so the remote push and the local write can never
// drift on which fields the owner's edit actually touched.
func applyUpdatePatch(entry *tracker.TrackEntry, upd *ent.TrackBindingUpdateOne, patch UpdatePatch) *ent.TrackBindingUpdateOne {
	if patch.Status != nil {
		entry.Status = *patch.Status
		upd = upd.SetStatus(*patch.Status)
	}
	if patch.LastChapterRead != nil {
		entry.Progress = *patch.LastChapterRead
		upd = upd.SetLastChapterRead(*patch.LastChapterRead)
	}
	if patch.Score != nil {
		entry.Score = *patch.Score
		upd = upd.SetScore(*patch.Score)
	}
	if patch.StartDate != nil {
		entry.StartDate = patch.StartDate
		upd = upd.SetStartDate(*patch.StartDate)
	}
	if patch.FinishDate != nil {
		entry.FinishDate = patch.FinishDate
		upd = upd.SetFinishDate(*patch.FinishDate)
	}
	if patch.Private != nil {
		entry.Private = *patch.Private
		upd = upd.SetPrivate(*patch.Private)
	}
	return upd
}
