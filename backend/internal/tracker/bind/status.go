package bind

import "github.com/technobecet/tsundoku/internal/tracker"

// defaultBindStatus returns trackerID's OWN native "currently reading"
// status string — the default a fresh SaveEntry seeds when Bind creates a
// brand-new remote entry for a manga that isn't on the account's list yet
// (Bind, service.go). Mirrors internal/tracker/syncsvc/status.go's
// completedStatus in shape (a per-tracker native-vocabulary switch), but for
// the OPPOSITE end of the lifecycle: the status a fresh bind should start
// at, not the one an auto-complete transitions to.
//
// An empty Status on that SaveEntry call would otherwise clobber the
// create: AniList's MediaListStatus GraphQL enum REJECTS "" outright
// (failing the bind), and Kitsu's JSON:API status field has no sane empty
// default either — see the tracker-push data-corruption fix this accompanies
// (internal/tracker/syncsvc/entry.go's baseEntryFromBinding).
//
// A tracker absent from this table (MangaUpdates) returns "": its
// list-based model has no status STRING at all — which LIST a series
// belongs to IS its status, and SaveEntry's caller (Bind) never has a list
// to place it in at create time, so leaving Status unset is correct here,
// not a gap.
func defaultBindStatus(trackerID int) string {
	switch trackerID {
	case tracker.IDAniList:
		// AniList's MediaListStatus enum.
		return "CURRENT"
	case tracker.IDMAL:
		// MAL's my_list_status.status enum.
		return "reading"
	case tracker.IDKitsu:
		// Kitsu's libraryEntry.status enum.
		return "current"
	default:
		return ""
	}
}
