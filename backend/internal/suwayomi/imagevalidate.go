// Package suwayomi — page image validation for the download path.
//
// This file provides validateImagePage, the guard that proves a fetched page is
// a complete, fully-decodable image BEFORE it is allowed into a CBZ. It exists
// because the owner's hard invariant is "never save a chapter with a broken or
// missing panel": a truncated body, an HTML challenge page served with HTTP 200,
// or a 0-byte body all pass the transport-level checks in PageBytes and would
// otherwise be written into the CBZ as a broken panel. Any page that fails this
// proof is turned into an error by the Fetcher, so the existing all-or-nothing
// fetch + per-source retry + cross-source fall-through drives the chapter to a
// COMPLETE download instead of persisting the break.
package suwayomi

import (
	"bytes"
	"errors"
	"fmt"
	"image"

	// Register the standard-library image decoders for their side effect: a blank
	// import wires each format into image.Decode's format registry. These cover
	// the overwhelming majority of manga pages.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	// WebP is heavily used by manga sources; x/image/webp registers a decoder for
	// both lossy (VP8) and lossless (VP8L) WebP. Without it a valid WebP page would
	// be false-rejected — worse than the bug we are fixing.
	_ "golang.org/x/image/webp"
)

// ErrBrokenPage is returned when a fetched page is not a complete, decodable image
// (empty, truncated, or non-image content such as an HTML challenge page). The
// Fetcher wraps it so a broken page fails the whole chapter attempt cleanly, which
// the per-source retry + fall-through machinery then drives to a complete download.
var ErrBrokenPage = errors.New("suwayomi: page failed image validation")

// validateImagePage proves that data is a complete, fully-decodable image before
// it may enter a CBZ. It is deliberately CONTENT-based (it inspects the bytes),
// never header/extension-based, so a lying or absent Content-Type cannot smuggle a
// broken panel through.
//
// The check, in order:
//   - G3: a 0-byte body is rejected (a 200 with an empty body currently slips past
//     PageBytes' non-2xx check).
//   - G1/G2: full image.Decode is the strongest proof — it reads the ENTIRE pixel
//     stream, so a truncated body (valid magic, short data) and an HTML page served
//     as 200 both fail here. DecodeConfig alone is NOT enough: a truncated body has
//     a valid header but short pixel data, which is exactly the missing-panel case.
//   - A format Go cannot decode in-process (currently AVIF) must NOT be false-
//     rejected — a valid AVIF page is a real panel. It is accepted on a strict
//     container-magic check instead (see isAcceptedUndecodable). This is the
//     deliberate trade-off: for formats we can decode we prove every pixel; for the
//     one we can't we prove the container, never dropping a real page.
func validateImagePage(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("%w: empty body", ErrBrokenPage)
	}

	if _, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		return nil
	} else if !isAcceptedUndecodable(data) {
		return fmt.Errorf("%w: %v", ErrBrokenPage, err)
	}

	return nil
}

// isAcceptedUndecodable reports whether data is a valid image container in a format
// Go's registered decoders cannot read in-process, which we accept on a strict
// magic-byte check rather than drop a real panel. Currently that is AVIF only.
func isAcceptedUndecodable(data []byte) bool {
	return isAVIF(data)
}

// isAVIF reports whether data is an AVIF image by its ISO-BMFF container magic: a
// 4-byte box size, the "ftyp" box type at offset 4, then an AVIF major brand at
// offset 8. Go's stdlib and x/image cannot decode AVIF, so a valid AVIF page must
// be accepted here to avoid false-rejecting a real panel.
func isAVIF(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	if string(data[4:8]) != "ftyp" {
		return false
	}
	switch string(data[8:12]) {
	case "avif", "avis":
		return true
	default:
		return false
	}
}
