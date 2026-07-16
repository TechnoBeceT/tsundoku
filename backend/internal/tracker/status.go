package tracker

// Status is Tsundoku's CANONICAL tracker-status vocabulary — the small,
// provider-agnostic set of reading-lifecycle states the sync engine reasons
// about internally (currently just "currently reading" and "read-complete";
// extend the enum + nativeStatusTable together when a caller needs more).
//
// It exists so the per-tracker NATIVE status strings live in exactly ONE
// place (nativeStatusTable) instead of being hardcoded across
// bind.defaultBindStatus + syncsvc.{completedStatus,readingStatus,
// propagatedCompletedStatus}. This mirrors the reference ports' (Mihon/
// Komikku/Suwayomi) own `toTrackStatus` per-service status map, inverted for
// this codebase's direction: those ports translate a tracker's native status
// INTO their canonical enum on read; Tsundoku STORES + PUSHES native strings
// (spec: "store native scale/codes; convert only at display"), so this table
// resolves the canonical enum OUT to each tracker's native string.
//
// 🔴 "read-complete" (StatusCompleted) is READING completion — the user has
// read every chapter — NOT release/publication completion. The two are
// different axes; nothing here reads the local Series.completed library flag.
type Status int

const (
	// StatusReading is "currently reading" — the state a fresh bind starts at.
	StatusReading Status = iota + 1
	// StatusCompleted is "read-complete" — every chapter read.
	StatusCompleted
)

// nativeStatusTable maps each tracker's canonical Status to its OWN native
// status string. A (tracker, status) pair absent from the table has no native
// string for that state and reports ok=false via NativeStatus.
//
// MangaUpdates is deliberately partial: it has NO status STRING at all — an
// entry's "status" there IS which numbered LIST it belongs to (see
// mangaupdates/mapper.go's listStatusLabels). Its Complete list's label
// ("complete") is the one canonical mapping that matters for cross-tracker
// completion propagation (syncsvc.CompleteSeries moves a MangaUpdates entry to
// that list). It has no "reading" native string — its reading state is the
// default list, represented as "" — so StatusReading is absent for it, exactly
// preserving bind.defaultBindStatus's original "" for MangaUpdates.
var nativeStatusTable = map[int]map[Status]string{
	IDAniList: {
		// AniList's MediaListStatus GraphQL enum.
		StatusReading:   "CURRENT",
		StatusCompleted: "COMPLETED",
	},
	IDMAL: {
		// MAL's my_list_status.status enum.
		StatusReading:   "reading",
		StatusCompleted: "completed",
	},
	IDKitsu: {
		// Kitsu's libraryEntry.status enum.
		StatusReading:   "current",
		StatusCompleted: "completed",
	},
	IDMangaUpdates: {
		// MangaUpdates' Complete-list label (list id 2) — see the table doc.
		StatusCompleted: "complete",
	},
}

// NativeStatus returns trackerID's OWN native status string for the canonical
// status s. ok is false when the tracker (or the specific state) has no native
// string — the caller then skips setting a status for it rather than guessing.
func NativeStatus(trackerID int, s Status) (native string, ok bool) {
	byStatus, ok := nativeStatusTable[trackerID]
	if !ok {
		return "", false
	}
	native, ok = byStatus[s]
	return native, ok
}
