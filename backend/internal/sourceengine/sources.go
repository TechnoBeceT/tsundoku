package sourceengine

import "context"

// Sources calls GET /sources to list every source the engine host has
// loaded from its installed extensions.
func (c *httpClient) Sources(ctx context.Context) ([]Source, error) {
	return get[[]Source](ctx, c, "/sources")
}
