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
	"encoding/binary"
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
var ErrBrokenPage = errors.New("suwayomi: page failed image validation")

// validateImagePage proves that data is a complete, fully-decodable image before
// it may enter a CBZ. It is deliberately CONTENT-based (it inspects the bytes),
// never header/extension-based, so a lying or absent Content-Type cannot smuggle a
// broken panel through.
//
// The check, in order:
//   - G3: a 0-byte body is rejected (a 200 with an empty body currently slips past
//     PageBytes' non-2xx check).
//   - Decompression-bomb guard: a cheap DecodeConfig (header-only, no pixel
//     allocation) reads the declared dimensions; an area over maxTotalPixels is
//     rejected BEFORE the full decode would allocate gigabytes.
//   - G1/G2: full image.Decode is the strongest proof — it reads the ENTIRE pixel
//     stream, so a truncated body (valid magic, short data) and an HTML page served
//     as 200 both fail here. DecodeConfig alone is NOT enough: a truncated body has
//     a valid header but short pixel data, which is exactly the missing-panel case.
//   - A format Go cannot decode in-process (currently AVIF) must NOT be false-
//     rejected — a valid AVIF page is a real panel. It is accepted on a strict
//     container-magic check instead (see isAcceptedUndecodable). This is the
//     deliberate trade-off: for formats we can decode we prove every pixel; for the
//     one we can't we prove the container, never dropping a real page.
//
// KNOWN, ACCEPTED false-rejects (do NOT add carve-outs): animated WebP (x/image/webp
// cannot decode it) and JPEG XL (no Go decoder, and no magic carve-out here) fail as
// broken. Both are ~nonexistent as manga pages, and a false-reject only fails a
// chapter (safe re: the never-save-a-broken-panel invariant) rather than saving a
// broken one — the correct side of the trade.
func validateImagePage(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("%w: empty body", ErrBrokenPage)
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
