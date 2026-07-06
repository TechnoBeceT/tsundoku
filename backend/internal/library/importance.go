package library

// belowExistingImportances returns `count` importances for newly attached sources,
// each strictly below the series' current-min importance (decision E: new sources
// gap-fill, never outrank already-satisfied chapters, so no upgrade re-download
// fires). Index 0 is the highest of the new batch. With no existing providers it
// falls back to the Adopt-wizard scale (count-i)*10. Importance is a bare
// comparable int, so values <= 0 are legal.
func belowExistingImportances(existing []int, count int) []int {
	if count <= 0 {
		return []int{}
	}
	out := make([]int, count)
	if len(existing) == 0 {
		for i := 0; i < count; i++ {
			out[i] = (count - i) * 10
		}
		return out
	}
	minExisting := existing[0]
	for _, v := range existing[1:] {
		if v < minExisting {
			minExisting = v
		}
	}
	for i := 0; i < count; i++ {
		out[i] = minExisting - (i+1)*10
	}
	return out
}
