package sync

// NextPush decides whether local reading progress should be pushed to a
// bound tracker, and what value to push. It implements the umbrella spec §6
// "never regress" rule: a push is warranted ONLY when the local furthest-
// read chapter is STRICTLY GREATER than the tracker's own last-read value —
// an equal or lower local number is never sent, so a stale/slow client (or
// a local re-scan that lands on an old chapter) can never drag a tracker's
// progress backward.
//
// localFurthest is the highest chapter number Tsundoku has locally marked
// read for the bound series (already filtered through SyncableNumbers by
// the caller — this function does not itself validate parseability).
// remoteLastRead is the tracker's own reported progress (TrackEntry.
// Progress, native scale).
//
// When shouldPush is false, push is meaningless (0) — callers must check
// shouldPush before using push.
func NextPush(localFurthest, remoteLastRead float64) (push float64, shouldPush bool) {
	if localFurthest > remoteLastRead {
		return localFurthest, true
	}
	return 0, false
}
