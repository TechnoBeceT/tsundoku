package syncsvc

import "github.com/technobecet/tsundoku/internal/tracker"

// muCompletedLabel is MangaUpdates' native "completed" status — the label of
// its Complete list (list id 2). This MIRRORS internal/tracker/mangaupdates'
// own listStatusLabels[2] value rather than importing it, the same
// "each ent-touching package keeps its own copy of the tiny per-tracker table"
// convention readingStatus/completedStatus already document.
const muCompletedLabel = "complete"

// propagatedCompletedStatus returns the native completed-status string to
// STORE (and push) when completion is PROPAGATED across a series' trackers
// (CompleteSeries, BUG-4 / QCAT-243). It EXTENDS completedStatus with
// MangaUpdates' own list-based completed label ("complete", the Complete
// list). Unlike completedStatus — used by the auto-complete-on-reach-total
// push, which deliberately leaves MangaUpdates untouched because it cannot
// know a total for it — completion propagation is an EXPLICIT terminal signal
// that SHOULD move even a totalless tracker to completed. ok is false only for
// a tracker not in the table (an unregistered id), so the caller skips it.
func propagatedCompletedStatus(trackerID int) (status string, ok bool) {
	if s, done := completedStatus(trackerID); done {
		return s, true
	}
	if trackerID == tracker.IDMangaUpdates {
		return muCompletedLabel, true
	}
	return "", false
}

// isPropagatedCompletedStatus reports whether status is trackerID's OWN
// completed label (see propagatedCompletedStatus) — used by UpdateTrack to
// detect an owner edit that TRANSITIONS a binding to completed and therefore
// must fan the completion out to the series' other trackers.
func isPropagatedCompletedStatus(trackerID int, status string) bool {
	completed, ok := propagatedCompletedStatus(trackerID)
	return ok && status == completed
}

// completedStatus returns the native "completed" status string in trackerID's
// OWN vocabulary — consulted ONLY when sync.ShouldAutoComplete fires
// (phase-4 spec §2: "auto-COMPLETED ONLY when the tracker reported a
// NON-ZERO total AND last_read == total"). Status is otherwise ALWAYS
// native + provider-opaque and never normalized by this package (spec §2:
// "store native scale/codes; convert only at display") — this is the ONE
// deliberate exception, the same well-known-per-tracker-constant shape
// bind.remoteURLFor already uses for canonical URLs.
//
// A tracker absent from this table is skipped by callers (pushOne only sets
// entry.Status when ok is true) rather than guessed at: MangaUpdates has no
// status STRING at all — an entry's "status" there IS which LIST it belongs
// to (see mangaupdates/mapper.go's listStatusLabels), and moving a series
// between lists is an operation this port's UpdateEntry does not expose. A
// MangaUpdates binding's progress/dates still advance on auto-complete;
// only its native status string is left as last pulled.
func completedStatus(trackerID int) (status string, ok bool) {
	switch trackerID {
	case tracker.IDAniList:
		// AniList's MediaListStatus enum.
		return "COMPLETED", true
	case tracker.IDMAL:
		// MAL's my_list_status.status enum.
		return "completed", true
	case tracker.IDKitsu:
		// Kitsu's libraryEntry.status enum.
		return "completed", true
	default:
		return "", false
	}
}

// isCompletedStatus reports whether status is trackerID's OWN native
// "completed" string (see completedStatus). Consulted by SetSeriesProgress
// (QCAT-242) to decide whether a regressing owner reset must reopen a
// binding: a tracker absent from completedStatus's table (MangaUpdates)
// always reports false here — it has no status STRING to compare against.
func isCompletedStatus(trackerID int, status string) bool {
	completed, ok := completedStatus(trackerID)
	return ok && status == completed
}

// readingStatus returns the native "currently reading" status string in
// trackerID's OWN vocabulary — consulted ONLY by SetSeriesProgress
// (QCAT-242) when an explicit owner reset regresses a binding whose current
// status is completedStatus's own value: reopening it must land back on the
// SAME per-tracker vocabulary completedStatus writes, not a guess.
//
// This intentionally DUPLICATES internal/tracker/bind/status.go's
// defaultBindStatus (identical values, opposite end of the lifecycle — a
// fresh bind's starting status there vs. a regressed reopen here) rather
// than importing it: this package already sits above internal/tracker/bind
// (see this package's own doc comment), and defaultBindStatus's own doc
// comment already established the "each ent-touching package keeps its own
// copy of this tiny per-tracker table" convention rather than adding a
// cross-package coupling for three string constants.
//
// A tracker absent from this table (MangaUpdates) returns ok=false: its
// list-based model has no status STRING at all (see completedStatus's own
// doc comment), so SetSeriesProgress leaves status untouched on a
// regression for that tracker — only progress moves back.
func readingStatus(trackerID int) (status string, ok bool) {
	switch trackerID {
	case tracker.IDAniList:
		// AniList's MediaListStatus enum.
		return "CURRENT", true
	case tracker.IDMAL:
		// MAL's my_list_status.status enum.
		return "reading", true
	case tracker.IDKitsu:
		// Kitsu's libraryEntry.status enum.
		return "current", true
	default:
		return "", false
	}
}
