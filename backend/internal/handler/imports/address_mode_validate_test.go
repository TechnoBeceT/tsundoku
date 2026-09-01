package imports

import (
	"errors"
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

func TestValidateAdoptAddressModeCompatibilityAndRejection(t *testing.T) {
	base := adoptRequestBody{Title: "Mode", Providers: []adoptProviderRequest{{Source: "42", URL: "/manga/mode", Importance: 1}}}
	if err := validateAdoptBody(base); err != nil {
		t.Fatalf("omitted addressMode should remain compatible: %v", err)
	}
	base.Providers[0].AddressMode = sourceengine.AddressMode("unsupported")
	if err := validateAdoptBody(base); err == nil {
		t.Fatal("invalid addressMode was accepted")
	}
}

func TestParseAddressContextCompatibilityAndRejection(t *testing.T) {
	mode, webURL, err := parseAddressContext("", "")
	if err != nil || mode != sourceengine.AddressModeUnknown || webURL != "" {
		t.Fatalf("omitted address context = (%q, %q, %v), want unknown compatibility default", mode, webURL, err)
	}
	_, _, err = parseAddressContext("unsupported", "https://source.test/manga/mode")
	var httpErr *echo.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("invalid query addressMode error = %v, want HTTP 400", err)
	}
}
