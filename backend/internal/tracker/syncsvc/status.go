package syncsvc

import "github.com/technobecet/tsundoku/internal/tracker"

// propagatedCompletedStatus returns the native completed-status string to
// STORE (and push) when completion is PROPAGATED across a series' trackers
// (CompleteSeries, BUG-4 / QCAT-243). It resolves the canonical
// tracker.StatusCompleted straight through the ONE per-service native-status
// table (tracker.NativeStatus), which INCLUDES MangaUpdates' own list-based
// completed label ("complete", its Complete list) — unlike completedStatus
// below, which deliberately excludes it. Completion propagation is an EXPLICIT
// terminal signal that SHOULD move even a totalless tracker (MangaUpdates) to
// completed; the auto-complete-on-reach-total push cannot, because it can
// never know a total for it. ok is false only for a tracker not in the table
// (an unregistered id), so the caller skips it.
func propagatedCompletedStatus(trackerID int) (status string, ok bool) {
	return tracker.NativeStatus(trackerID, tracker.StatusCompleted)
}

// isPropagatedCompletedStatus reports whether status is trackerID's OWN
// completed label (see propagatedCompletedStatus) — used by UpdateTrack to
// detect an owner edit that TRANSITIONS a binding to completed and therefore
// must fan the completion out to the series' other trackers, and by
// SyncNow's cross-tracker read-completion reconcile to detect that a PULL has
// landed a completed status on any binding.
func isPropagatedCompletedStatus(trackerID int, status string) bool {
	completed, ok := propagatedCompletedStatus(trackerID)
	return ok && status == completed
}

// completedStatus returns the native "completed" status string in trackerID's
// OWN vocabulary — consulted ONLY when sync.ShouldAutoComplete fires
// (phase-4 spec §2: "auto-COMPLETED ONLY when the tracker reported a
// NON-ZERO total AND last_read == total"). It resolves the canonical
// tracker.StatusCompleted through tracker.NativeStatus but DELIBERATELY
// EXCLUDES MangaUpdates: MangaUpdates has no status STRING at all — an entry's
// "status" there IS which LIST it belongs to (see mangaupdates/mapper.go's
// listStatusLabels) — and, reporting no total, it can never satisfy the
// auto-complete-on-reach-total rule this feeds anyway. Its completion is
// driven exclusively by the explicit fan-out (propagatedCompletedStatus).
//
// A tracker absent from this carve-out is skipped by callers (pushOne only
// sets entry.Status when ok is true). A MangaUpdates binding's progress/dates
// still advance on auto-complete; only its native status string is left as
// last pulled here.
func completedStatus(trackerID int) (status string, ok bool) {
	if trackerID == tracker.IDMangaUpdates {
		return "", false
	}
	return tracker.NativeStatus(trackerID, tracker.StatusCompleted)
}

// isCompletedStatus reports whether status is trackerID's OWN native
// "completed" string (see completedStatus). Consulted by SetSeriesProgress
// (QCAT-242) to decide whether a regressing owner reset must reopen a
// binding: a tracker absent from completedStatus's carve-out (MangaUpdates)
// always reports false here — it has no status STRING to compare against.
func isCompletedStatus(trackerID int, status string) bool {
	completed, ok := completedStatus(trackerID)
	return ok && status == completed
}

// readingStatus returns the native "currently reading" status string in
// trackerID's OWN vocabulary — consulted ONLY by SetSeriesProgress
// (QCAT-242) when an explicit owner reset regresses a binding whose current
// status is completedStatus's own value: reopening it must land back on the
// SAME per-tracker vocabulary completedStatus writes, not a guess. It resolves
// the canonical tracker.StatusReading through the shared tracker.NativeStatus
// table (the same table bind.defaultBindStatus resolves — identical values,
// opposite end of the lifecycle).
//
// A tracker with no native reading string (MangaUpdates — tracker.NativeStatus
// reports ok=false) returns ok=false: its list-based model has no status
// STRING at all (see completedStatus's own doc comment), so SetSeriesProgress
// leaves status untouched on a regression for that tracker — only progress
// moves back.
func readingStatus(trackerID int) (status string, ok bool) {
	return tracker.NativeStatus(trackerID, tracker.StatusReading)
}
