package settings

import (
	"net/http"

	"github.com/labstack/echo/v4"

	settingssvc "github.com/technobecet/tsundoku/internal/settings"
)

// UpdateItem is one key/value pair in a PATCH /api/settings request body.
type UpdateItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// UpdateRequest is the PATCH /api/settings body: a batch of key/value updates
// applied all-or-nothing.
type UpdateRequest struct {
	Settings []UpdateItem `json:"settings"`
}

// validateUpdate checks the request SHAPE (a non-empty list with non-empty keys)
// and maps it to the service's KeyValue slice. The allowlist + per-key bounds are
// enforced by the service (ErrUnknownSetting / ErrInvalidSetting), so this layer
// only rejects structurally-malformed requests. An empty list or a blank key is
// a 400.
func validateUpdate(req UpdateRequest) ([]settingssvc.KeyValue, error) {
	if len(req.Settings) == 0 {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "settings must contain at least one key/value")
	}
	out := make([]settingssvc.KeyValue, len(req.Settings))
	for i, it := range req.Settings {
		if it.Key == "" {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "each setting must have a non-empty key")
		}
		out[i] = settingssvc.KeyValue{Key: it.Key, Value: it.Value}
	}
	return out, nil
}
