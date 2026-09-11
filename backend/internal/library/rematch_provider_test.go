package library

import (
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

func TestValidateRematchRefRequiresSameSourceAndValidAddress(t *testing.T) {
	tests := []struct {
		name   string
		stored string
		ref    ProviderRef
		want   error
	}{
		{name: "same source", stored: "42", ref: ProviderRef{Source: "42", URL: "/comics/title", AddressMode: sourceengine.AddressModeDirect}},
		{name: "source mismatch", stored: "42", ref: ProviderRef{Source: "43", URL: "/comics/title", AddressMode: sourceengine.AddressModeDirect}, want: ErrProviderSourceMismatch},
		{name: "bad mode", stored: "42", ref: ProviderRef{Source: "42", URL: "/comics/title", AddressMode: sourceengine.AddressMode("bad")}, want: ErrInvalidProviderAddress},
		{name: "empty url", stored: "42", ref: ProviderRef{Source: "42", AddressMode: sourceengine.AddressModeDirect}, want: ErrInvalidProviderAddress},
		{name: "unlinked provider", stored: "disk", ref: ProviderRef{Source: "1", URL: "/comics/title", AddressMode: sourceengine.AddressModeDirect}, want: ErrSourceNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateRematchRef(tt.stored, tt.ref)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}
