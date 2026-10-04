// Package searchstream renders request-scoped progressive search responses.
package searchstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/imports"
)

const writeTimeout = 5 * time.Second

// Requested validates the optional streaming selector; JSON remains the default.
func Requested(c echo.Context) (bool, error) {
	switch c.QueryParam("stream") {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, echo.NewHTTPError(http.StatusBadRequest, "stream must be true or false")
	}
}

// Serve writes serialized search snapshots with bounded socket backpressure. Errors before the
// first snapshot retain their normal HTTP status; a committed stream ends with a safe error frame.
func Serve(c echo.Context, run func(context.Context, func(imports.SearchSnapshotDTO) error) error) error {
	ctx, cancel := context.WithCancel(c.Request().Context())
	defer cancel()
	response := c.Response()
	controller := http.NewResponseController(response.Writer)
	var writeErr error
	write := func(event string, value any) error { return writeFrame(ctx, response, controller, event, value) }
	err := run(ctx, func(snapshot imports.SearchSnapshotDTO) error {
		writeErr = write("search", snapshot)
		if writeErr != nil {
			cancel()
		}
		return writeErr
	})
	if err == nil {
		return nil
	}
	if !response.Committed {
		return err
	}
	if writeErr == nil && !errors.Is(err, context.Canceled) {
		_ = write("error", map[string]string{"message": "Search could not complete"})
	}
	return nil
}

func writeFrame(ctx context.Context, response *echo.Response, controller *http.ResponseController, event string, value any) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = controller.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	defer func() {
		if resetErr := controller.SetWriteDeadline(time.Time{}); err == nil {
			err = resetErr
		}
	}()
	if !response.Committed {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Cache-Control", "no-cache")
		response.Header().Set("X-Accel-Buffering", "no")
		response.WriteHeader(http.StatusOK)
	}
	if _, err = fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	return controller.Flush()
}
