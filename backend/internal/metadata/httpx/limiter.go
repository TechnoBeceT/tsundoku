// Package httpx holds cross-provider HTTP plumbing for internal/metadata's
// provider clients (AniList, MangaDex, ...) — currently just the shared
// rate-limiting http.RoundTripper every provider client wraps its transport
// in, so each public metadata API's documented per-minute request cap is
// honored without duplicating throttle logic per provider.
package httpx

import (
	"net/http"

	"golang.org/x/time/rate"
)

// rateLimitedTransport wraps a base http.RoundTripper with a per-minute
// token-bucket throttle shared across every request issued through it.
type rateLimitedTransport struct {
	base    http.RoundTripper
	limiter *rate.Limiter
}

// NewRateLimited wraps base in an http.RoundTripper that blocks each
// RoundTrip until a token is available from a perMinute-capacity budget,
// refilled continuously at perMinute/60 tokens per second. base defaults to
// http.DefaultTransport when nil. The bucket burst is capped at 1 so
// requests are spaced evenly rather than allowed to spike to the whole
// per-minute allowance at once — the conservative choice for a public API
// with an anti-abuse cap (e.g. AniList's documented 90 req/min). A
// non-positive perMinute is treated as 1 request/minute rather than
// disabling the limit or dividing by zero.
func NewRateLimited(base http.RoundTripper, perMinute int) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if perMinute <= 0 {
		perMinute = 1
	}
	return &rateLimitedTransport{
		base:    base,
		limiter: rate.NewLimiter(rate.Limit(float64(perMinute))/60, 1),
	}
}

// RoundTrip blocks until the limiter admits req (honoring req's context for
// cancellation) then delegates to the base transport.
func (t *rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.limiter.Wait(req.Context()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}
