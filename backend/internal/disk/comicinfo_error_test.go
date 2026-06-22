package disk_test

import (
	"archive/zip"
	"os"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
)

// TestUnmarshalComicInfoBadXML verifies that UnmarshalComicInfo returns an
// error for malformed XML input.
func TestUnmarshalComicInfoBadXML(t *testing.T) {
	t.Parallel()

	_, err := disk.UnmarshalComicInfo([]byte("not xml at all <<<"))
	if err == nil {
		t.Error("UnmarshalComicInfo: expected error for malformed XML, got nil")
	}
}

// TestReadComicInfoFromCBZMissing verifies that ReadComicInfoFromCBZ returns an
// error when the target file does not exist.
func TestReadComicInfoFromCBZMissing(t *testing.T) {
	t.Parallel()

	_, err := disk.ReadComicInfoFromCBZ("/nonexistent/path/to/chapter.cbz")
	if err == nil {
		t.Error("ReadComicInfoFromCBZ: expected error for missing file, got nil")
	}
}

// TestReadComicInfoFromCBZNoEntry verifies that ReadComicInfoFromCBZ returns
// nil (no error) for a valid CBZ that contains no ComicInfo.xml entry.
func TestReadComicInfoFromCBZNoEntry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dest := dir + "/no-ci.cbz"

	// Build a minimal ZIP with one image entry but no ComicInfo.xml.
	f, err := os.Create(dest) //nolint:gosec // test-only, dest is from t.TempDir()
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	w := zip.NewWriter(f)
	ent, err := w.CreateHeader(&zip.FileHeader{Name: "001.jpg", Method: zip.Store})
	if err != nil {
		t.Fatalf("create header: %v", err)
	}
	if _, err := ent.Write([]byte{0xFF, 0xD8}); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	got, err := disk.ReadComicInfoFromCBZ(dest)
	if err != nil {
		t.Fatalf("ReadComicInfoFromCBZ: %v", err)
	}
	if got != nil {
		t.Errorf("ReadComicInfoFromCBZ: want nil for CBZ with no ComicInfo.xml, got %+v", got)
	}
}

// TestUpdateCBZComicInfoMissing verifies that UpdateCBZComicInfo returns an
// error for a non-existent CBZ file.
func TestUpdateCBZComicInfoMissing(t *testing.T) {
	t.Parallel()

	err := disk.UpdateCBZComicInfo("/nonexistent/path/to/chapter.cbz", disk.ComicInfo{})
	if err == nil {
		t.Error("UpdateCBZComicInfo: expected error for missing file, got nil")
	}
}

// TestReadSidecarCorrupt verifies that ReadSidecar returns an error when the
// tsundoku.json file contains malformed JSON.
func TestReadSidecarCorrupt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	jsonPath := dir + "/tsundoku.json"
	if err := os.WriteFile(jsonPath, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := disk.ReadSidecar(dir)
	if err == nil {
		t.Error("ReadSidecar: expected error for corrupt JSON, got nil")
	}
}
