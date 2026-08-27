package sourceengine

import "context"

// Status calls GET /status for a bounded, payload-free runtime snapshot.
func (c *httpClient) Status(ctx context.Context) (EngineStatus, error) {
	return get[EngineStatus](ctx, c, "/status")
}
