// Package suwayomi_test — unit tests for the page image validation guard.
//
// Every "valid" case uses REAL image bytes (see testimages_test.go); the decode is
// never faked. This pins the owner's "never save a broken/missing panel" invariant
// at the smallest unit: a page is accepted iff it is a complete, decodable image
// (or a valid AVIF container Go cannot decode in-process).
package suwayomi_test

import (
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// TestValidateImagePage_AcceptsRealImages verifies that fully-decodable JPEG, PNG,
// and WebP pages — and a valid AVIF container we accept on magic — all pass. A
// false-reject here silently fails real chapters, the worst outcome, so this is the
// #1 guard.
func TestValidateImagePage_AcceptsRealImages(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"jpeg": validJPEG(t),
		"png":  validPNG(t),
		"webp": validWebP(t),
		"avif": validAVIF(t),
	}
	for name, data := range cases {
		if err := suwayomi.ValidateImagePage(data); err != nil {
			t.Errorf("%s: valid image rejected: %v", name, err)
		}
	}
}

// TestValidateImagePage_RejectsBrokenContent verifies every broken-panel shape the
// audit enumerates is rejected as ErrBrokenPage: an empty body (G3), a truncated
// image (G1 — valid magic, short body), and an HTML page served as 200 (G2).
func TestValidateImagePage_RejectsBrokenContent(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"empty body":     {},
		"truncated jpeg": truncatedJPEG(t),
		"html as 200":    htmlPage(),
		"garbage bytes":  {0xAA, 0xBB, 0xCC, 0xDD},
	}
	for name, data := range cases {
		err := suwayomi.ValidateImagePage(data)
		if err == nil {
			t.Errorf("%s: broken content accepted, want rejection", name)
			continue
		}
		if !errors.Is(err, suwayomi.ErrBrokenPage) {
			t.Errorf("%s: err %v does not wrap ErrBrokenPage", name, err)
		}
	}
}
