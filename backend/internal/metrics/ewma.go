package metrics

import (
	"math"

	"github.com/technobecet/tsundoku/internal/ent"
)

// ewmaAlpha is the smoothing factor of the exponentially-weighted moving average
// of per-source search latency: newEwma = alpha*sample + (1-alpha)*prev. At 0.3
// a single slow sample moves the average noticeably (so a source that just went
// cold is flagged quickly) while a lone spike does not dominate the history.
const ewmaAlpha = 0.3

// NextEwma returns the updated EWMA latency (milliseconds) given the previous
// EWMA and a new latency sample. The FIRST sample seeds the average directly: a
// non-positive prev (a fresh/never-measured row) means there is no history to
// blend, so the sample becomes the average. Otherwise it blends by ewmaAlpha and
// rounds to the nearest millisecond (the field is an int).
func NextEwma(prevEwma, sampleMs int) int {
	if prevEwma <= 0 {
		return sampleMs
	}
	return int(math.Round(ewmaAlpha*float64(sampleMs) + (1-ewmaAlpha)*float64(prevEwma)))
}

// IsSlow reports whether a source metric is "slow" relative to thresholdMs. A nil
// metric (a source that has never been measured) counts as slow so the warm-up
// job seeds it on the next pass. Otherwise a source is slow when its rolling
// EWMA latency is STRICTLY greater than the threshold.
func IsSlow(m *ent.SourceMetric, thresholdMs int) bool {
	if m == nil {
		return true
	}
	return m.EwmaLatencyMs > thresholdMs
}
