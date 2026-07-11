package disk_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
)

// pngMagic / jpegMagic / webpBytes are minimal but signature-valid image byte
// blobs so http.DetectContentType (the extension-lookup fallback) still yields
// the right type even on a host whose mime tables lack .webp.
var (
	jpegMagic = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	pngMagic  = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	webpBytes = append(append([]byte("RIFF"), []byte{0x1A, 0, 0, 0}...), []byte("WEBPVP8 ")...)
)

// zipEntry is one file to place in a synthetic CBZ.
type zipEntry struct {
	name string
	data []byte
}

// makeCBZ writes a zip archive at a temp path from the given entries (verbatim
// names, no padding) so a test can control ordering + non-image members exactly.
func makeCBZ(t *testing.T, entries []zipEntry) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "chapter.cbz")
	f, err := os.Create(path) //nolint:gosec // path is under t.TempDir(), not user input.
	if err != nil {
		t.Fatalf("create cbz: %v", err)
	}
	w := zip.NewWriter(f)
	for _, e := range entries {
		ew, err := w.Create(e.name)
		if err != nil {
			t.Fatalf("create entry %q: %v", e.name, err)
		}
		if _, err := ew.Write(e.data); err != nil {
			t.Fatalf("write entry %q: %v", e.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	return path
}

func TestListCBZPages_OrderAndExclusions(t *testing.T) {
	t.Parallel()
	// Deliberately unpadded + out of order, with ComicInfo.xml and a non-image
	// entry interleaved, to prove natural ordering and exclusion.
	path := makeCBZ(t, []zipEntry{
		{"10.jpg", jpegMagic},
		{"2.jpg", jpegMagic},
		{"1.jpg", jpegMagic},
		{"ComicInfo.xml", []byte("<ComicInfo/>")},
		{"notes.txt", []byte("ignore me")},
	})

	names, err := disk.ListCBZPages(path)
	if err != nil {
		t.Fatalf("ListCBZPages: %v", err)
	}
	want := []string{"1.jpg", "2.jpg", "10.jpg"}
	if len(names) != len(want) {
		t.Fatalf("ListCBZPages: got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("ListCBZPages order: got %v, want %v", names, want)
		}
	}
}

func TestReadCBZPage_BytesAndContentType(t *testing.T) {
	t.Parallel()
	path := makeCBZ(t, []zipEntry{
		{"1.jpg", jpegMagic},
		{"2.png", pngMagic},
		{"3.webp", webpBytes},
		{"ComicInfo.xml", []byte("<ComicInfo/>")},
	})

	cases := []struct {
		index    int
		wantData []byte
		wantType string
	}{
		{0, jpegMagic, "image/jpeg"},
		{1, pngMagic, "image/png"},
		{2, webpBytes, "image/webp"},
	}
	for _, tc := range cases {
		data, ct, err := disk.ReadCBZPage(path, tc.index)
		if err != nil {
			t.Fatalf("ReadCBZPage(%d): %v", tc.index, err)
		}
		if !bytes.Equal(data, tc.wantData) {
			t.Fatalf("ReadCBZPage(%d): bytes = %v, want %v", tc.index, data, tc.wantData)
		}
		if !strings.HasPrefix(ct, tc.wantType) {
			t.Fatalf("ReadCBZPage(%d): content-type = %q, want prefix %q", tc.index, ct, tc.wantType)
		}
	}
}

func TestReadCBZPage_OutOfRange(t *testing.T) {
	t.Parallel()
	path := makeCBZ(t, []zipEntry{
		{"1.jpg", jpegMagic},
		{"ComicInfo.xml", []byte("<ComicInfo/>")},
	})

	for _, idx := range []int{-1, 1, 99} {
		if _, _, err := disk.ReadCBZPage(path, idx); !errors.Is(err, disk.ErrPageOutOfRange) {
			t.Fatalf("ReadCBZPage(%d): want ErrPageOutOfRange, got %v", idx, err)
		}
	}
}

func TestReadCBZPage_MissingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "does-not-exist.cbz")
	_, _, err := disk.ReadCBZPage(path, 0)
	if err == nil {
		t.Fatal("ReadCBZPage: want error for missing file, got nil")
	}
	if errors.Is(err, disk.ErrPageOutOfRange) {
		t.Fatalf("ReadCBZPage: missing file should not be ErrPageOutOfRange, got %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadCBZPage: want wrapped os.ErrNotExist, got %v", err)
	}
}

func TestListCBZPages_MissingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "does-not-exist.cbz")
	if _, err := disk.ListCBZPages(path); err == nil {
		t.Fatal("ListCBZPages: want error for missing file, got nil")
	}
}
