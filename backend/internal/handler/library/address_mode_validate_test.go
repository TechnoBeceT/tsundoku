package library

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

func TestValidateProviderRefAddressModeCompatibilityAndRejection(t *testing.T) {
	body := providerRefBody{Source: "42", URL: "/manga/mode"}
	if err := validateProviderRef(body); err != nil {
		t.Fatalf("omitted addressMode should remain compatible: %v", err)
	}
	body.AddressMode = sourceengine.AddressMode("unsupported")
	if err := validateProviderRef(body); err == nil {
		t.Fatal("invalid addressMode was accepted")
	}
}
