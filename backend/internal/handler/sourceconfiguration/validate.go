package sourceconfiguration

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
)

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
