package sourceengine

import (
	"context"
	"net/http"
)

// reposRequest/reposResponse share the same wire shape ({"repos": [...]})
// for both the PUT request body and every GET/PUT response.
type reposRequest struct {
	Repos []string `json:"repos"`
}

type reposResponse struct {
	Repos []string `json:"repos"`
}

// Repos calls GET /repos to read the configured extension-repo index URLs.
func (c *httpClient) Repos(ctx context.Context) ([]string, error) {
	res, err := get[reposResponse](ctx, c, "/repos")
	if err != nil {
		return nil, err
	}
	return res.Repos, nil
}

// SetRepos calls PUT /repos to REPLACE the configured extension-repo index
// URL list and returns it read back. An empty slice clears every repo.
func (c *httpClient) SetRepos(ctx context.Context, repos []string) ([]string, error) {
	res, err := doJSON[reposResponse](ctx, c, http.MethodPut, "/repos", reposRequest{Repos: repos})
	if err != nil {
		return nil, err
	}
	return res.Repos, nil
}
