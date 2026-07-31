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
	"strings"
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

// rejectPhraseCase is one "this body must be rejected, and the message must name
// THIS sub-cause" expectation.
type rejectPhraseCase struct {
	name   string
	data   []byte
	phrase string
}

// runRejectPhraseCases asserts, for every case, all three things a reject-wording
// contract needs: the body is rejected at all, the error wraps ErrBrokenPage (so
// the accept/reject DECISION is unchanged), and the message carries the phrase
// errorclass keys off.
//
// Only the assertion body is shared — each caller keeps its own table, because
// WHICH bodies map to WHICH wording is the contract that test exists to pin, and
// merging the tables would hide that. Sharing the loop is what stops two tables
// that pin the same three properties from drifting in how strictly they check.
func runRejectPhraseCases(t *testing.T, cases []rejectPhraseCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := sourceengine.ValidateImagePage(tc.data)
			if err == nil {
				t.Fatalf("%s: accepted, want rejection", tc.name)
			}
			if !errors.Is(err, sourceengine.ErrBrokenPage) {
				t.Errorf("%s: err %v does not wrap ErrBrokenPage", tc.name, err)
			}
			if !strings.Contains(err.Error(), tc.phrase) {
				t.Errorf("%s: err %q does not contain %q", tc.name, err.Error(), tc.phrase)
			}
		})
	}
}

// TestValidateImagePage_ClassifiesRejectReason verifies the reject MESSAGE names the
// actual sub-cause (not a raw decoder error that reads like a format bug). Each case
// still wraps ErrBrokenPage — the DECISION is unchanged; only the wording is. The
// exact phrases are load-bearing: errorclass keys off them ("challenge" → captcha;
// "incomplete image"/"empty response"/"unrecognized image data" → broken_image).
func TestValidateImagePage_ClassifiesRejectReason(t *testing.T) {
	t.Parallel()

	runRejectPhraseCases(t, []rejectPhraseCase{
		// A valid JPEG signature (FF D8 FF) with the pixel stream cut → truncation.
		{"truncated jpeg", truncatedJPEG(t), "incomplete image"},
		// An HTML challenge/interstitial served as 200 → an anti-bot page.
		{"html challenge", htmlPage(), "challenge"},
		// A 0-byte body → the source returned nothing.
		{"empty body", []byte{}, "empty response"},
		// Bytes with no known image signature and not markup → unrecognized.
		{"random non-image", []byte{0xAA, 0xBB, 0xCC, 0xDD}, "unrecognized image data"},
	})
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

// TestValidateImagePage_SignatureBoundaries pins the magic-number recognition that
// decides WHICH reject wording an undecodable body gets: a known raster signature
// means the header arrived and the pixel stream was cut ("incomplete image",
// transient and worth retrying), while no signature means the body was never an
// image at all ("unrecognized image data"). Both wrap ErrBrokenPage, so the accept/
// reject decision is identical either way — what this pins is the errorclass-facing
// sub-cause, which drives what the Source Health Console tells the owner.
//
// Every case is a SHORTEST-possible body, so each one exercises exactly one
// signature and its length/offset boundary rather than a whole decodable image. The
// near-misses are the load-bearing half: a two-byte SOI (one short of JPEG's
// three-byte test), a JPEG SOI whose third byte is not a marker, a RIFF container
// whose payload tag at offset 8 is WAVE rather than WEBP, and a bare "RIFF" too
// short to carry that tag — each must fall through to "unrecognized", proving the
// checks are anchored to both a minimum length and a fixed offset.
func TestValidateImagePage_SignatureBoundaries(t *testing.T) {
	t.Parallel()

	const (
		truncated    = "incomplete image"
		unrecognized = "unrecognized image data"
	)

	runRejectPhraseCases(t, []rejectPhraseCase{
		// Recognised signatures, pixel stream absent → a truncated image.
		{"png magic only", []byte{0x89, 0x50, 0x4E, 0x47}, truncated},
		{"gif magic only", []byte("GIF"), truncated},
		{"bmp magic only", []byte("BM"), truncated},
		{"riff webp header only", []byte("RIFF\x00\x00\x00\x00WEBP"), truncated},

		// Near-misses: no signature holds, so the body is not an image at all.
		{"jpeg soi one byte short", []byte{0xFF, 0xD8}, unrecognized},
		{"jpeg soi without marker byte", []byte{0xFF, 0xD8, 0x00}, unrecognized},
		{"riff carrying wave not webp", []byte("RIFF\x00\x00\x00\x00WAVE"), unrecognized},
		{"riff tag alone", []byte("RIFF"), unrecognized},
		{"bmp magic one byte short", []byte("B"), unrecognized},
	})
}
