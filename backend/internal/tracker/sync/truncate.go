package sync

import "math"

// TruncateForInteger floors a local fractional chapter number to the
// nearest whole chapter below it, for pushing to a tracker whose wire field
// is an integer chapter COUNT (both AniList and MAL — see tracker.TrackEntry.
// Progress's doc comment). A fractional local chapter (e.g. an omake
// numbered 12.5) truncates DOWN to 12: the tracker cannot represent "half a
// chapter read", and rounding UP would falsely claim chapter 13 was read.
func TruncateForInteger(n float64) int {
	return int(math.Floor(n))
}
