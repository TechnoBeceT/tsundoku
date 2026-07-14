package sync

// Converge implements the umbrella spec §6 "conflict = MAX wins BOTH
// directions" rule: when local and remote progress disagree, both sides
// settle on the HIGHER of the two — never the lower, and never a blended
// value. A caller that owns the local chapter rows marks every chapter
// numbered <= converged as read; a caller that owns the tracker connection
// pushes converged to the remote ONLY when converged > remoteLastRead (i.e.
// local was ahead — reuse NextPush for that decision, this function only
// computes the target both sides move to).
func Converge(localFurthest, remoteLastRead float64) (converged float64) {
	if localFurthest > remoteLastRead {
		return localFurthest
	}
	return remoteLastRead
}
