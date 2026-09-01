package enginehost

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// newHTTPHealthProber builds the production HealthProber: a GET <baseURL>/health
// with its own short per-probe timeout, additionally bounded by the caller's
// lifecycle context. A 200 means ready (nil); any transport error or non-200
// status is "not ready yet" (a non-nil error), which the caller retries on the
// next poll tick while its single launch budget remains.
func newHTTPHealthProber(timeout time.Duration) HealthProber {
	client := &http.Client{Timeout: timeout}
	return func(ctx context.Context, baseURL string) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
		if err != nil {
			return fmt.Errorf("enginehost: build health probe: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("enginehost: health probe %s: %w", baseURL, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("enginehost: health probe %s: status %d", baseURL, resp.StatusCode)
		}
		return nil
	}
}
