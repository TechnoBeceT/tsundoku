package bind

import "github.com/technobecet/tsundoku/internal/tracker"

// defaultBindStatus returns trackerID's OWN native "currently reading"
// status string — the default a fresh SaveEntry seeds when Bind creates a
// brand-new remote entry for a manga that isn't on the account's list yet
// (Bind, service.go). It resolves the canonical tracker.StatusReading through
// the ONE per-service native-status table (tracker.NativeStatus) so the
// native strings ("CURRENT"/"reading"/"current") live in a single place, not
// hardcoded here (§2 DRY).
//
// An empty Status on that SaveEntry call would otherwise clobber the
// create: AniList's MediaListStatus GraphQL enum REJECTS "" outright
// (failing the bind), and Kitsu's JSON:API status field has no sane empty
// default either — see the tracker-push data-corruption fix this accompanies
// (internal/tracker/syncsvc/entry.go's baseEntryFromBinding).
//
// A tracker with no native reading string (MangaUpdates — tracker.NativeStatus
// reports ok=false) returns "": its list-based model has no status STRING at
// all — which LIST a series belongs to IS its status, and SaveEntry's caller
// (Bind) never has a list to place it in at create time, so leaving Status
// unset is correct here, not a gap.
func defaultBindStatus(trackerID int) string {
	native, _ := tracker.NativeStatus(trackerID, tracker.StatusReading)
	return native
}
