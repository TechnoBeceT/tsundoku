package syncsvc

import "github.com/technobecet/tsundoku/internal/tracker"

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
