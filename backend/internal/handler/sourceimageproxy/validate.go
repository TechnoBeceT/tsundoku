package sourceimageproxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
)

type updateRequest struct {
	Enabled bool
}

func parseSourceID(raw string) (int64, error) {
	if !isSignedDecimal(raw) {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "sourceId must be numeric")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "sourceId must be numeric")
	}
	return id, nil
}

func isSignedDecimal(raw string) bool {
	if raw == "" {
		return false
	}
	start := 0
	if raw[0] == '-' {
		if len(raw) == 1 {
			return false
		}
		start = 1
	}
	for i := start; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return false
		}
	}
	return true
}

func decodeUpdateRequest(body io.Reader) (updateRequest, error) {
	decoder := json.NewDecoder(body)
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return updateRequest{}, echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return updateRequest{}, echo.NewHTTPError(http.StatusBadRequest, "request body must contain one JSON object")
	}
	raw, ok := fields["enabled"]
	if !ok {
		return updateRequest{}, echo.NewHTTPError(http.StatusBadRequest, "enabled is required")
	}
	if len(fields) != 1 {
		return updateRequest{}, echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	var enabled *bool
	if err := json.Unmarshal(raw, &enabled); err != nil || enabled == nil {
		return updateRequest{}, echo.NewHTTPError(http.StatusBadRequest, "enabled must be a boolean")
	}
	return updateRequest{Enabled: *enabled}, nil
}
