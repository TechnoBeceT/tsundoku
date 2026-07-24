// Package sourceengine — page image validation for the download path.
//
// This file provides validateImagePage, the guard that proves a fetched page is
// a complete, fully-decodable image BEFORE it is allowed into a CBZ. It exists
// because the owner's hard invariant is "never save a chapter with a broken or
// missing panel": a truncated body, an HTML challenge page served with HTTP 200,
// or a 0-byte body all pass the transport-level checks in Client.Image and would
// otherwise be written into the CBZ as a broken panel. Any page that fails this
// proof is turned into an error by the Fetcher, so the existing all-or-nothing
// fetch + per-source retry + cross-source fall-through drives the chapter to a
// COMPLETE download instead of persisting the break.
//
// Restored (GAP-083) from the retired internal/suwayomi/imagevalidate.go, which
// was deleted along with the rest of internal/suwayomi in the P2 engine-host
// migration (commit d4545df) without carrying the guard over to the new
// internal/sourceengine.Fetcher — this file closes that gap.
package sourceengine

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"

	// image/jpeg is imported by NAME (not blank) so we can reference
	// jpeg.UnsupportedError in the decode-failure carve-out below. A named import
	// still runs the package init() that registers the JPEG decoder into
	// image.Decode's format registry, so JPEG decoding is unaffected.
	"image/jpeg"

	// Register the remaining standard-library image decoders for their side effect:
	// a blank import wires each format into image.Decode's format registry. These
	// cover the overwhelming majority of manga pages.
	_ "image/gif"
	_ "image/png"

	// WebP is heavily used by manga sources; x/image/webp registers a decoder for
	// both lossy (VP8) and lossless (VP8L) WebP. Without it a valid WebP page would
	// be false-rejected — worse than the bug we are fixing.
	_ "golang.org/x/image/webp"
)

// maxTotalPixels caps the total pixel area (width*height) an image may declare
// before the full decode is even attempted. It exists to defuse a decompression
// bomb: a small compressed body can declare enormous dimensions (e.g. a ~100KB PNG
// claiming 30000x30000 ≈ 900 MP), and image.Decode would then allocate ~pixels*4
// bytes (≈3.6GB), which — times DownloadConcurrency — OOMs the process.
//
// The cap is on TOTAL pixels, deliberately NOT on either side: webtoon long-strip
// pages are legitimately huge in ONE dimension (e.g. 800x30000 ≈ 24 MP). 300 MP
// admits any realistic strip while rejecting a square bomb (900 MP).
const maxTotalPixels = 300_000_000

// ErrBrokenPage is returned when a fetched page is not a complete, decodable image
// (empty, truncated, oversized, or non-image content such as an HTML challenge
// page). The Fetcher wraps it so a broken page fails the whole chapter attempt
// cleanly, which the per-source retry + fall-through machinery then drives to a
// complete download.
var ErrBrokenPage = errors.New("sourceengine: page failed image validation")

// validateImagePage proves that data is a complete, fully-decodable image before
// it may enter a CBZ. It is deliberately CONTENT-based (it inspects the bytes),
// never header/extension-based, so a lying or absent Content-Type cannot smuggle a
// broken panel through.
//
// The check, in order:
//   - G3: a 0-byte body is rejected (a 200 with an empty body currently slips past
//     the transport-level checks in Client.Image).
//   - Decompression-bomb guard: a cheap DecodeConfig (header-only, no pixel
//     allocation) reads the declared dimensions; an area over maxTotalPixels is
//     rejected BEFORE the full decode would allocate gigabytes.
//   - G1/G2: full image.Decode is the strongest proof — it reads the ENTIRE pixel
//     stream, so a truncated body (valid magic, short data) and an HTML page served
//     as 200 both fail here. DecodeConfig alone is NOT enough: a truncated body has
//     a valid header but short pixel data, which is exactly the missing-panel case.
//   - A real panel Go cannot decode in-process must NOT be false-rejected. Two
//     shapes qualify (see isAcceptedUndecodable): a valid AVIF container (Go has no
//     AVIF decoder), accepted on a strict container-magic check; and a structurally
//     complete JPEG whose chroma-subsampling ratio Go's image/jpeg does not
//     implement (e.g. 3x1 / H3V1 — golang/go#62421), accepted on a
//     jpeg.UnsupportedError plus a complete SOI...EOI frame. This is the deliberate
//     trade-off: for what we can decode we prove every pixel; for what we can't we
//     prove structural completeness, never dropping a real page. Truncation is still
//     rejected — a cut tail has no trailing EOI, and ordinary corruption surfaces as
//     a jpeg.FormatError rather than jpeg.UnsupportedError.
//
// The DECISION is unchanged from before this file's reword: every body that was
// rejected is still rejected, every accepted body still accepted. What changed is
// the reject WORDING — a raw decode error like "webp: invalid format" LOOKS like a
// format bug, when the cause is almost always a transient DELIVERY failure (a
// truncated download, an empty body, or a Cloudflare/anti-bot HTML page returned in
// place of the image, under load). classifyBrokenPage (below) names the actual
// sub-cause so the Source Health Console can classify and diagnose it correctly:
//   - empty response  — the source returned no bytes (transient);
//   - anti-bot/HTML challenge page — errorclass reads its "challenge" wording as
//     captcha, the actionable cause;
//   - incomplete image — a known image signature whose pixel stream was truncated
//     (errorclass → broken_image);
//   - unrecognized image data — no known signature and not a challenge.
//
// KNOWN, ACCEPTED false-rejects (do NOT add carve-outs): animated WebP (x/image/webp
// cannot decode it) and JPEG XL (no Go decoder, and no magic carve-out here) fail as
// broken. Both are ~nonexistent as manga pages, and a false-reject only fails a
// chapter (safe re: the never-save-a-broken-panel invariant) rather than saving a
// broken one — the correct side of the trade.
func validateImagePage(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("%w: empty response — the source returned no image data (transient; will retry)", ErrBrokenPage)
	}

	// Header-only dimension pre-check: cheap and bomb-proof. A format we cannot even
	// read the config for (AVIF) fails here silently and falls through to the
	// accept-on-magic path below; a decodable image with absurd dimensions is
	// rejected before the full decode allocates its pixel buffer.
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		if int64(cfg.Width)*int64(cfg.Height) > maxTotalPixels {
			return fmt.Errorf("%w: image too large (%dx%d)", ErrBrokenPage, cfg.Width, cfg.Height)
		}
	}

	if _, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		return nil
	} else if !isAcceptedUndecodable(data, err) {
		return classifyBrokenPage(data)
	}

	return nil
}

// classifyBrokenPage names the sub-cause of a body that failed image.Decode and is
// NOT an accepted-undecodable real panel. It exists so the reject message describes
// the actual (almost always transient, delivery-side) cause instead of leaking a raw
// decoder error that reads like a format bug. It changes only the wording — the
// caller has already made the reject DECISION; every message wraps ErrBrokenPage.
// Order matters: a challenge page can carry image-looking bytes, so it is checked
// first; then a known image signature (truncation); else unrecognized.
func classifyBrokenPage(data []byte) error {
	switch {
	case looksLikeChallenge(data):
		return fmt.Errorf("%w: not an image — the source returned an anti-bot/HTML challenge page instead of the image (will retry)", ErrBrokenPage)
	case hasImageSignature(data):
		return fmt.Errorf("%w: incomplete image — the download was truncated before the full image arrived (will retry)", ErrBrokenPage)
	default:
		return fmt.Errorf("%w: unrecognized image data — not a supported image format", ErrBrokenPage)
	}
}

// looksLikeChallenge reports whether data is an anti-bot / HTML interstitial served
// in place of the image (Cloudflare "just a moment", an "attention required" wall, a
// CAPTCHA page) rather than a real image. It is a heuristic on the leading bytes: an
// HTML/markup body starts with '<' once leading whitespace is trimmed, and the common
// challenge phrases appear near the top of the body. The word "challenge" in the
// resulting message is load-bearing — errorclass reads it as captcha (the actionable
// cause), not broken_image. Only the first ~2KB is scanned; a challenge page always
// declares itself in its head.
func looksLikeChallenge(data []byte) bool {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '<' {
		return true
	}

	head := data
	if len(head) > 2048 {
		head = head[:2048]
	}
	lower := bytes.ToLower(head)
	for _, marker := range challengeMarkers {
		if bytes.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// challengeMarkers are the lowercased phrases an anti-bot/HTML challenge page carries
// near the top of its body. Kept beside looksLikeChallenge as the single list.
var challengeMarkers = [][]byte{
	[]byte("<!doctype"),
	[]byte("<html"),
	[]byte("just a moment"),
	[]byte("cloudflare"),
	[]byte("attention required"),
	[]byte("access denied"),
	[]byte("captcha"),
}

// hasImageSignature reports whether data begins with a known raster-image magic
// number (JPEG, PNG, GIF, RIFF/WEBP, or BMP). A body with a valid signature that
// still fails image.Decode is a TRUNCATED image — the header arrived but the pixel
// stream was cut — which is the "incomplete image" delivery failure, distinct from a
// body that is not an image at all.
func hasImageSignature(data []byte) bool {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return true // JPEG (SOI + marker)
	case len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47:
		return true // PNG
	case len(data) >= 3 && data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46:
		return true // GIF ("GIF")
	case len(data) >= 12 && data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50:
		return true // RIFF container carrying WEBP ("RIFF"..."WEBP")
	case len(data) >= 2 && data[0] == 0x42 && data[1] == 0x4D:
		return true // BMP ("BM")
	default:
		return false
	}
}

// isAcceptedUndecodable reports whether data is a real panel Go's registered decoders
// cannot read in-process, which we accept rather than drop. Two shapes qualify: a
// valid AVIF container (strict magic-byte check — Go has no AVIF decoder), and a
// structurally complete JPEG that failed decode only on an unsupported subsampling
// ratio (isCompleteButUnsupportedJPEG). The AVIF check ignores decodeErr; the JPEG
// check needs it to tell a valid-but-unsupported JPEG apart from a corrupt one.
func isAcceptedUndecodable(data []byte, decodeErr error) bool {
	return isAVIF(data) || isCompleteButUnsupportedJPEG(data, decodeErr)
}

// isCompleteButUnsupportedJPEG reports whether data is a structurally complete JPEG
// that image.Decode rejected only because Go's image/jpeg does not implement its
// chroma-subsampling ratio (e.g. 3x1 / H3V1) — a KNOWN stdlib limitation
// (golang/go#62421), NOT corruption. Such a page is a real, complete panel every
// browser renders, so false-rejecting it fails an otherwise-downloadable chapter:
// the same "accept a real panel Go can't decode in-process" case as AVIF.
//
// BOTH conditions are required, and together they preserve the never-save-a-broken-
// panel invariant:
//   - the decode error is a jpeg.UnsupportedError — proving "valid JPEG, unsupported
//     FEATURE", distinct from truncation/corruption (which surface as a
//     jpeg.FormatError or io.ErrUnexpectedEOF), AND
//   - the body is a complete JPEG frame (SOI...EOI, see isCompleteJPEG) — proving it
//     is not truncated. A truncated unusual-subsampling JPEG throws the SAME
//     UnsupportedError at SOF-parse time, before the missing tail matters, so the
//     error check ALONE would let a truncated body through; the trailing-EOI check
//     is what rejects it.
//
// Trade-off (identical in spirit to the AVIF container-accept): a complete-framed but
// internally-corrupt such-JPEG could pass. That is vanishingly rare and, like AVIF,
// the accepted cost of never dropping a real page.
func isCompleteButUnsupportedJPEG(data []byte, decodeErr error) bool {
	var unsupported jpeg.UnsupportedError
	return errors.As(decodeErr, &unsupported) && isCompleteJPEG(data)
}

// isCompleteJPEG reports whether data is a structurally complete JPEG: the start-of-
// image marker 0xFF 0xD8 at the very start AND the end-of-image marker 0xFF 0xD9 as
// the final two bytes. The trailing-EOI check is the truncation guard — a JPEG whose
// tail was cut mid-stream cannot end in 0xFF 0xD9, so a truncated body fails here
// even when its header still parses.
func isCompleteJPEG(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	soi := data[0] == 0xFF && data[1] == 0xD8
	eoi := data[len(data)-2] == 0xFF && data[len(data)-1] == 0xD9
	return soi && eoi
}

// isAVIF reports whether data is an AVIF image by its ISO-BMFF container magic: a
// 4-byte big-endian box size, the "ftyp" box type at offset 4, then a brand list
// (major_brand at offset 8, minor_version at 12, then compatible_brands from 16).
// A spec-valid AVIF may carry major_brand=mif1 with "avif" only in the compatible
// list, so the WHOLE brand list is scanned, not just the major brand. Go's stdlib
// and x/image cannot decode AVIF, so a valid AVIF page must be accepted here to
// avoid false-rejecting a real panel.
func isAVIF(data []byte) bool {
	if len(data) < 16 || string(data[4:8]) != "ftyp" {
		return false
	}

	// Clamp the brand scan to what we actually hold and the declared box length,
	// whichever is smaller (a large body only needs its ftyp box read).
	end := int(binary.BigEndian.Uint32(data[0:4]))
	if end <= 0 || end > len(data) {
		end = len(data)
	}

	// major_brand at [8:12].
	if isAVIFBrand(data[8:12]) {
		return true
	}
	// compatible_brands are 4-byte codes starting at offset 16 ([12:16] is the
	// minor_version integer, not a brand).
	for off := 16; off+4 <= end; off += 4 {
		if isAVIFBrand(data[off : off+4]) {
			return true
		}
	}
	return false
}

// isAVIFBrand reports whether a 4-byte FourCC is an AVIF brand (still image or
// image sequence).
func isAVIFBrand(brand []byte) bool {
	switch string(brand) {
	case "avif", "avis":
		return true
	default:
		return false
	}
}
