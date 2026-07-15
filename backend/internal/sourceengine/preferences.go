package sourceengine

import (
	"context"
	"fmt"
	"net/http"
)

// preferencesResponse is the wire envelope both GET and PUT
// /sources/{id}/preferences wrap their result in ({"preferences": [...]}).
type preferencesResponse struct {
	Preferences []Preference `json:"preferences"`
}

// preferencesPath builds the /sources/{id}/preferences path for sourceID.
func preferencesPath(sourceID int64) string {
	return fmt.Sprintf("/sources/%d/preferences", sourceID)
}

// Preferences calls GET /sources/{id}/preferences to read sourceID's
// configurable preferences.
func (c *httpClient) Preferences(ctx context.Context, sourceID int64) ([]Preference, error) {
	res, err := get[preferencesResponse](ctx, c, preferencesPath(sourceID))
	if err != nil {
		return nil, err
	}
	return res.Preferences, nil
}

// SetPreferences calls PUT /sources/{id}/preferences, sending changes as a
// raw {key: value} JSON map (NOT wrapped), and returns the full refreshed
// preference list from the response.
func (c *httpClient) SetPreferences(ctx context.Context, sourceID int64, changes map[string]any) ([]Preference, error) {
	res, err := doJSON[preferencesResponse](ctx, c, http.MethodPut, preferencesPath(sourceID), changes)
	if err != nil {
		return nil, err
	}
	return res.Preferences, nil
}
