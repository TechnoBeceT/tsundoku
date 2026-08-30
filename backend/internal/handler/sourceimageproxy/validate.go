package sourceimageproxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

type updateRequest struct {
	Enabled bool
}

type wireUpdateRequest struct {
	Enabled *bool `json:"enabled"`
}

func parseSourceID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "sourceId must be numeric")
	}
	return id, nil
}

func decodeUpdateRequest(body io.Reader) (updateRequest, error) {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var wire wireUpdateRequest
	if err := decoder.Decode(&wire); err != nil {
		return updateRequest{}, echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return updateRequest{}, echo.NewHTTPError(http.StatusBadRequest, "request body must contain one JSON object")
	}
	if wire.Enabled == nil {
		return updateRequest{}, echo.NewHTTPError(http.StatusBadRequest, "enabled is required")
	}
	return updateRequest{Enabled: *wire.Enabled}, nil
}
