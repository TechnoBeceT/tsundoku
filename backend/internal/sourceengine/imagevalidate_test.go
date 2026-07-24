// Package sourceengine_test — unit tests for the page image validation guard.
//
// Every "valid" case uses REAL image bytes (see testimages_test.go); the decode is
// never faked. This pins the owner's "never save a broken/missing panel" invariant
// at the smallest unit: a page is accepted iff it is a complete, decodable image
// (or a valid AVIF container Go cannot decode in-process).
//
// Ported from the retired internal/suwayomi/imagevalidate_test.go (GAP-083).
package sourceengine_test

import (
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// TestValidateImagePage_AcceptsRealImages verifies that fully-decodable JPEG, PNG,
// lossy (VP8) and lossless (VP8L) WebP pages — and a valid AVIF container we accept
// on magic — all pass. A false-reject here silently fails real chapters, the worst
// outcome, so this is the #1 guard.
func TestValidateImagePage_AcceptsRealImages(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"jpeg":          validJPEG(t),
		"png":           validPNG(t),
		"webp lossy":    validWebP(t),
		"webp lossless": validWebPLossless(t),
		"avif":          validAVIF(t),
	}
	for name, data := range cases {
		if err := sourceengine.ValidateImagePage(data); err != nil {
			t.Errorf("%s: valid image rejected: %v", name, err)
		}
	}
}

// TestValidateImagePage_DimensionCap verifies the decompression-bomb guard: a small
// body declaring an absurd total area is rejected BEFORE a full decode, while a
// legitimate webtoon long-strip page (huge in one dimension, modest total pixels) is
// ACCEPTED — proving the cap is on total area, never per-side.
func TestValidateImagePage_DimensionCap(t *testing.T) {
	t.Parallel()

	// 30000x30000 = 900 MP ≫ the 300 MP cap: a decompression bomb, rejected.
	err := sourceengine.ValidateImagePage(dimensionBombPNG(t, 30000, 30000))
	if err == nil {
		t.Fatal("dimension bomb (30000x30000) accepted, want rejection")
	}
	if !errors.Is(err, sourceengine.ErrBrokenPage) {
		t.Errorf("dimension bomb: err %v does not wrap ErrBrokenPage", err)
	}

	// 800x20000 = 16 MP: a legitimate long-strip page — huge height, well under the
	// total-area cap. Must be ACCEPTED (a per-side cap would wrongly reject it).
	if err := sourceengine.ValidateImagePage(tallStripPNG(t, 800, 20000)); err != nil {
		t.Errorf("legitimate 800x20000 long-strip page rejected: %v", err)
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
		err := sourceengine.ValidateImagePage(data)
		if err == nil {
			t.Errorf("%s: broken content accepted, want rejection", name)
			continue
		}
		if !errors.Is(err, sourceengine.ErrBrokenPage) {
			t.Errorf("%s: err %v does not wrap ErrBrokenPage", name, err)
		}
	}
}

// TestValidateImagePage_AcceptsUnsupportedSubsamplingJPEG verifies a complete JPEG
// with a chroma-subsampling ratio Go's image/jpeg cannot decode (3x1 / H3V1,
// golang/go#62421) is ACCEPTED. It is a real, complete panel every browser renders;
// false-rejecting it fails an otherwise-downloadable chapter — the same accept-a-
// real-panel-Go-can't-decode case as AVIF.
func TestValidateImagePage_AcceptsUnsupportedSubsamplingJPEG(t *testing.T) {
	t.Parallel()

	if err := sourceengine.ValidateImagePage(subsampled3x1JPEG(t)); err != nil {
		t.Errorf("complete 3x1-subsampled JPEG rejected: %v", err)
	}
}

// TestValidateImagePage_RejectsIncompleteUnsupportedJPEG verifies the accept-path's
// truncation guard: an unusual-subsampling JPEG whose trailing EOI has been cut is
// REJECTED, even though it raises the SAME jpeg.UnsupportedError as the complete one.
// The structural SOI...EOI completeness check — not the error class — is what draws
// the line, so a truncated (missing-panel) body can never ride the carve-out in.
func TestValidateImagePage_RejectsIncompleteUnsupportedJPEG(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"truncated 3x1 jpeg (no EOI)": truncatedSubsampled3x1JPEG(t),
		"soi-prefixed garbage":        soiPrefixedGarbage(),
	}
	for name, data := range cases {
		err := sourceengine.ValidateImagePage(data)
		if err == nil {
			t.Errorf("%s: incomplete content accepted, want rejection", name)
			continue
		}
		if !errors.Is(err, sourceengine.ErrBrokenPage) {
			t.Errorf("%s: err %v does not wrap ErrBrokenPage", name, err)
		}
	}
}
