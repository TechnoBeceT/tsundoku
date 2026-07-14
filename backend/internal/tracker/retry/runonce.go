package retry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entpendingtrackpush "github.com/technobecet/tsundoku/internal/ent/pendingtrackpush"
)

// RunOnce runs ONE bounded pass over every DUE pending push and returns how
// many rows it attempted (processed) — NOT how many succeeded; a row that
// failed and is still due-eligible next time is still counted here, since
// the caller (job.Runner.StartTrackerRetry) only needs "did this pass do
// anything" for logging, not a success/failure split.
//
// A row is DUE when BOTH hold:
//   - next_attempt_at is nil (never attempted) or <= now (backoff elapsed);
//   - attempts < maxAttempts (the hard retry cap — a row that has already
//     exhausted its budget is excluded from every future pass, so it is
//     never retried again; see the package doc comment's "bounded retries"
//     section).
//
// For each due row, pusher.Push is called in PER-ROW ISOLATION: one row's
// failure (a push error, or even a failure to persist the failure — logged,
// not propagated) never aborts the rest of the batch.
//
//   - SUCCESS: the row is deleted — the progress is safely on the tracker,
//     nothing left to retry.
//   - FAILURE: attempts increments, last_error is recorded, and
//     next_attempt_at is set to now + an exponential backoff (base 1m,
//     doubling per attempt, capped at 1h) — UNLESS the new attempts count
//     has now reached maxAttempts, in which case the row is left in place
//     (still updated with the failure info) but will no longer appear in
//     ANY future due-pass, per the attempts<maxAttempts filter above. This
//     is the never-lose-progress guarantee: a failed push NEVER deletes the
//     row — the pending chapter number stays intact in the database for the
//     owner to see (a tracking-health signal) or for a later manual re-push
//     to pick up.
//
// A hard error is returned only when the due-row query itself fails (a DB
// infrastructure failure) — individual row failures never surface as a
// RunOnce error.
func (q *Queue) RunOnce(ctx context.Context, pusher Pusher, now time.Time, maxAttempts int) (processed int, err error) {
	rows, err := q.client.PendingTrackPush.Query().
		Where(
			entpendingtrackpush.AttemptsLT(maxAttempts),
			entpendingtrackpush.Or(
				entpendingtrackpush.NextAttemptAtIsNil(),
				entpendingtrackpush.NextAttemptAtLTE(now),
			),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("retry.RunOnce: load due pending pushes: %w", err)
	}

	for _, row := range rows {
		processed++
		if pushErr := pusher.Push(ctx, row.TrackBindingID, row.Chapter); pushErr != nil {
			q.recordFailure(ctx, row, pushErr, now)
			continue
		}
		if delErr := q.client.PendingTrackPush.DeleteOne(row).Exec(ctx); delErr != nil {
			slog.WarnContext(ctx, "retry.RunOnce: delete pending push after successful push failed",
				"id", row.ID, "track_binding_id", row.TrackBindingID, "err", delErr)
		}
	}
	return processed, nil
}

// recordFailure persists one push failure onto row: attempts++, last_error,
// and a fresh backoff next_attempt_at. A persistence failure here is logged
// and swallowed — RunOnce's batch must not abort because one row's
// bookkeeping write failed (mirrors download.Dispatcher.bumpSourceFailure's
// same best-effort discipline for its own per-source retry state).
func (q *Queue) recordFailure(ctx context.Context, row *ent.PendingTrackPush, cause error, now time.Time) {
	newAttempts := row.Attempts + 1
	next := now.Add(backoffCurve(newAttempts))
	if _, uErr := row.Update().
		SetAttempts(newAttempts).
		SetLastError(cause.Error()).
		SetNextAttemptAt(next).
		Save(ctx); uErr != nil {
		slog.WarnContext(ctx, "retry.RunOnce: could not persist pending-push failure",
			"id", row.ID, "track_binding_id", row.TrackBindingID, "err", uErr)
	}
}

// backoffCurve returns the delay before the NEXT attempt after a row has
// just reached attempt failures: base×2^(attempt-1), capped at 1h. attempt=1
// (the first-ever failure) yields base=1m; attempt=2 yields 2m; and so on.
// This intentionally duplicates the small shape of
// download.Dispatcher.backoffCurve rather than importing internal/download
// — a cross-domain dependency for one helper function is not worth it, and
// the curve itself is trivial.
func backoffCurve(attempt int) time.Duration {
	const base = time.Minute
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 12 {
		shift = 12 // overflow guard: base(1m)×2^12 ≈ 68 days, already >> the 1h cap.
	}
	d := base * (1 << uint(shift)) //nolint:gosec // shift is capped at 12; base×2^12 stays well within int64.
	if d > time.Hour {
		d = time.Hour
	}
	return d
}
