package syncsvc

import (
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// baseEntryFromBinding seeds a tracker.TrackEntry from binding's CURRENTLY
// PERSISTED fields — RemoteID/LibraryID (how every concrete Tracker
// addresses the entry at all) plus Status/Score/Private/StartDate/
// FinishDate/TotalChapters.
//
// This is the fix for the pre-activation data-corruption blocker: every
// concrete Tracker client does a FULL-FIELD write, never a sparse PATCH —
// mal.upsertEntry (internal/tracker/mal/client.go) always sends
// score/status/dates from entry, anilist.UpdateEntry (internal/tracker/
// anilist/client.go + queries.go's updateEntryMutation) always sends
// scoreRaw/status/private, and kitsu.buildLibraryEntryRequest (internal/
// tracker/kitsu/mapper.go) always sends status/rating/private. A caller that
// builds a TrackEntry carrying only RemoteID/LibraryID/Progress therefore
// silently clobbers the remote's score → 0, private → false (flips to
// public), and status → "" — and AniList's MediaListStatus GraphQL enum
// REJECTS an empty string outright, failing every advance push.
//
// Callers own Progress themselves (never seeded here): a caller reaches for
// this helper precisely because Progress is the ONE field about to change,
// so baking in a stale value here would just be overwritten anyway. This
// mirrors update.go's UpdateTrack, which built this exact same field set by
// hand before this extraction (§2 DRY).
func baseEntryFromBinding(binding *ent.TrackBinding) tracker.TrackEntry {
	return tracker.TrackEntry{
		RemoteID:      binding.RemoteID,
		LibraryID:     binding.LibraryID,
		Status:        binding.Status,
		Score:         binding.Score,
		TotalChapters: binding.TotalChapters,
		StartDate:     binding.StartDate,
		FinishDate:    binding.FinishDate,
		Private:       binding.Private,
	}
}
