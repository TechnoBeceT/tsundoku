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

type repoTrustRequest struct {
	RepoURL           string `json:"repoUrl"`
	SignerFingerprint string `json:"signerFingerprint"`
}

type repoTrustResponse struct {
	Trust map[string]string `json:"trust"`
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

// RepoTrust calls the authenticated GET /repos/trust control RPC.
func (c *httpClient) RepoTrust(ctx context.Context) (map[string]string, error) {
	res, err := get[repoTrustResponse](ctx, c, "/repos/trust")
	if err != nil {
		return nil, err
	}
	return res.Trust, nil
}

// SetRepoTrust calls the authenticated PUT /repos/trust control RPC and
// returns the complete independently configured pin map read back.
func (c *httpClient) SetRepoTrust(ctx context.Context, repoURL, signerFingerprint string) (map[string]string, error) {
	res, err := doJSON[repoTrustResponse](ctx, c, http.MethodPut, "/repos/trust", repoTrustRequest{
		RepoURL:           repoURL,
		SignerFingerprint: signerFingerprint,
	})
	if err != nil {
		return nil, err
	}
	return res.Trust, nil
}
