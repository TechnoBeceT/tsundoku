package sourceengine

import (
	"context"
	"net/http"
)

// SetFlareSolverr calls PUT /config/flaresolverr with patch, sending only
// its non-nil fields (no-clobber — see FlareSolverrPatch's doc comment), and
// returns the config read back.
func (c *httpClient) SetFlareSolverr(ctx context.Context, patch FlareSolverrPatch) (FlareSolverrConfig, error) {
	return doJSON[FlareSolverrConfig](ctx, c, http.MethodPut, "/config/flaresolverr", patch)
}

// SetSocks calls PUT /config/socks with patch, sending only its non-nil
// fields, and returns the config read back (the password is never echoed
// back by the host).
func (c *httpClient) SetSocks(ctx context.Context, patch SocksPatch) (SocksConfig, error) {
	return doJSON[SocksConfig](ctx, c, http.MethodPut, "/config/socks", patch)
}
