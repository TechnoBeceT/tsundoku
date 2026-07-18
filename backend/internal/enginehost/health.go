package enginehost

import (
	"fmt"
	"net/http"
	"time"
)

// newHTTPHealthProber builds the production HealthProber: a GET <baseURL>/health
// with its own short per-probe timeout (independent of any reconcile context — a
// single probe must not hang a spawn's poll loop). A 200 means ready (nil); any
// transport error or non-200 status is "not ready yet" (a non-nil error), which
// the caller simply retries on the next poll tick.
func newHTTPHealthProber(timeout time.Duration) HealthProber {
	client := &http.Client{Timeout: timeout}
	return func(baseURL string) error {
		resp, err := client.Get(baseURL + "/health")
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
