package sync

// ShouldAutoComplete reports whether a bound tracker entry should be
// auto-transitioned to COMPLETED. Umbrella spec §6 / phase-4 spec §2: this
// is true ONLY when the tracker reported a NON-ZERO total chapter count AND
// the last-read chapter has reached (or passed) that total. An ongoing
// series the tracker reports with total 0 (unknown/still airing) must NEVER
// auto-complete, no matter how high lastRead climbs — 0 is "unknown", not
// "zero chapters".
func ShouldAutoComplete(lastRead, total float64) bool {
	return total > 0 && lastRead >= total
}
