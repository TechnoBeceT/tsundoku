package providerid_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/pkg/providerid"
)

// TestSourceID covers the whole rule: a LIVE provider's numeric source id parses
// (with surrounding whitespace tolerated), and anything else — most importantly a
// disk-origin display NAME — reports ok=false.
//
// The whitespace cases are not padding: a private copy of this parse in
// internal/refresh once omitted the trim, so " 8 " counted as disk-origin there
// and as a live source everywhere else. That divergence is why the rule now has a
// single implementation, and these rows are what keep it honest.
func TestSourceID(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		wantID   int64
		wantOK   bool
	}{
		{name: "live numeric id", provider: "99", wantID: 99, wantOK: true},
		{name: "live id with surrounding whitespace", provider: " 8 ", wantID: 8, wantOK: true},
		{name: "large 64-bit source id", provider: "6247824327478187651", wantID: 6247824327478187651, wantOK: true},
		{name: "zero is a valid id, not a miss", provider: "0", wantID: 0, wantOK: true},
		{name: "disk-origin display name", provider: "Asura Scans", wantOK: false},
		{name: "disk-origin name that starts with digits", provider: "1st Kiss Manga", wantOK: false},
		{name: "empty provider", provider: "", wantOK: false},
		{name: "whitespace only", provider: "   ", wantOK: false},
		{name: "decimal is not a source id", provider: "12.5", wantOK: false},
		{name: "overflows int64", provider: "99999999999999999999", wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := providerid.SourceID(tc.provider)
			if ok != tc.wantOK {
				t.Fatalf("SourceID(%q) ok = %v, want %v", tc.provider, ok, tc.wantOK)
			}
			if ok && id != tc.wantID {
				t.Fatalf("SourceID(%q) = %d, want %d", tc.provider, id, tc.wantID)
			}
		})
	}
}
