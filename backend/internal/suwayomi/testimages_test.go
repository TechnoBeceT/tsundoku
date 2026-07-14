// Package suwayomi_test — shared test image fixtures.
//
// These helpers produce REAL image bytes so the decode-validation tests exercise
// the actual decoders, never a faked decode. JPEG/PNG are encoded in-process;
// WebP/AVIF are embedded as base64 of tiny 2x2 images (WebP has no stdlib/x-image
// encoder, AVIF has no Go decoder — both are decode-guarded below so a corrupt
// literal fails loudly rather than silently weakening a test).
package suwayomi_test

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	// x/image/webp lets the guard test PROVE the embedded WebP literal is a real,
	// decodable WebP (not just that validateImagePage accepts it).
	"golang.org/x/image/webp"
)

// tinyImage returns a 2x2 image filled with one colour, the source for the
// in-process JPEG/PNG encodings.
func tinyImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 40, B: 40, A: 255})
		}
	}
	return img
}

// validJPEG returns the bytes of a real, fully-encoded 2x2 JPEG.
func validJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, tinyImage(), nil); err != nil {
		t.Fatalf("encode test JPEG: %v", err)
	}
	return buf.Bytes()
}

// validPNG returns the bytes of a real, fully-encoded 2x2 PNG.
func validPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, tinyImage()); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// webpBase64 is a real 2x2 lossy (VP8) WebP produced by ImageMagick. validWebP
// decode-guards it so a corrupt literal can never silently pass a validation test.
const webpBase64 = "UklGRjwAAABXRUJQVlA4IDAAAADQAQCdASoCAAIAAgA0JaACdLoB+AADsAD+8MQL/yC5YXXI1/8gP+QH/ID/+PIAAAA="

// validWebP returns real WebP bytes and asserts x/image/webp can decode them, so
// the fixture itself is proven valid before any test relies on it.
func validWebP(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(webpBase64)
	if err != nil {
		t.Fatalf("decode embedded WebP base64: %v", err)
	}
	if _, err := webp.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("embedded WebP fixture is not a valid WebP: %v", err)
	}
	return data
}

// avifBase64 is a real 2x2 AVIF produced by ImageMagick. Go cannot decode AVIF, so
// validAVIF only asserts the container magic (the exact property validateImagePage
// relies on to accept a format it cannot fully decode).
const avifBase64 = "AAAAHGZ0eXBhdmlmAAAAAG1pZjFhdmlmbWlhZgAAANZtZXRhAAAAAAAAACFoZGxyAAAAAAAAAABwaWN0AAAAAAAAAAAAAAAAAAAAACJpbG9jAAAAAERAAAEAAQAAAAAA+gABAAAAAAAAACcAAAAjaWluZgAAAAAAAQAAABVpbmZlAgAAAAABAABhdjAxAAAAAA5waXRtAAAAAAABAAAAVmlwcnAAAAA4aXBjbwAAAAxhdjFDgUBsAAAAABRpc3BlAAAAAAAAAAIAAAACAAAAEHBpeGkAAAAAAwwMDAAAABZpcG1hAAAAAAAAAAEAAQOBAgMAAAAvbWRhdBIACghYADa0BDQbhDIZGUeHhiGJpppmgAAAkD+bDGFCJm5Y5galFw=="

// validAVIF returns real AVIF bytes and asserts the ISO-BMFF ftyp/avif magic is
// present, so the fixture matches what isAVIF checks.
func validAVIF(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(avifBase64)
	if err != nil {
		t.Fatalf("decode embedded AVIF base64: %v", err)
	}
	if len(data) < 12 || string(data[4:8]) != "ftyp" || string(data[8:12]) != "avif" {
		t.Fatalf("embedded AVIF fixture lacks the ftyp/avif container magic")
	}
	return data
}

// truncatedJPEG returns a valid JPEG header followed by a short body — valid magic,
// missing pixel data. This is the missing-panel case DecodeConfig would miss but a
// full image.Decode catches.
func truncatedJPEG(t *testing.T) []byte {
	t.Helper()
	full := validJPEG(t)
	// Keep only the first 16 bytes: the SOI + APP0 marker survive (valid magic) but
	// the entropy-coded scan is gone, so a full decode fails.
	return full[:16]
}

// htmlPage returns the bytes of an HTML challenge/error page served with HTTP 200 —
// non-image content that must never be written as a panel.
func htmlPage() []byte {
	return []byte("<!DOCTYPE html>\n<html><head><title>Just a moment...</title></head>" +
		"<body>Checking your browser before accessing.</body></html>")
}
