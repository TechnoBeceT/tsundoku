package disk

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/technobecet/tsundoku/internal/fetcher"
)

// CreateCBZ writes a CBZ archive at destPath from pages and a ComicInfo.
//
// Pages are stored with zip.Store (no compression) for fast random access by
// Komga. ComicInfo.xml is stored with zip.Deflate. The write is atomic:
// the archive is written to a temporary file alongside the destination, then
// fsynced and renamed over it. If any step fails the temporary file is removed
// and no partial archive is visible at destPath.
//
// Pages are named by zero-padded index (1-based) followed by "." + Ext.
// fetcher.PageImage.Ext is a bare extension (e.g. "jpg") — a "." is prepended.
func CreateCBZ(destPath string, pages []fetcher.PageImage, ci ComicInfo) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o750); err != nil {
		return fmt.Errorf("disk.CreateCBZ: create directory: %w", err)
	}

	tmpPath := destPath + ".tmp"
	// G304: path constructed from a validated storage root — not a path traversal concern.
	f, err := os.Create(tmpPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("disk.CreateCBZ: create temp file: %w", err)
	}

	if err := writeZipContent(f, tmpPath, pages, ci); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		removeTmp(tmpPath)
		return fmt.Errorf("disk.CreateCBZ: rename to final path: %w", err)
	}

	return nil
}

// writeZipContent writes all pages and ComicInfo into f as a zip archive,
// then fsyncs and closes f. On any error the temp file is removed.
func writeZipContent(f *os.File, tmpPath string, pages []fetcher.PageImage, ci ComicInfo) error {
	w := zip.NewWriter(f)

	if err := writePages(w, pages); err != nil {
		closeAndClean(w, f, tmpPath)
		return err
	}

	if err := writeComicInfo(w, ci); err != nil {
		closeAndClean(w, f, tmpPath)
		return err
	}

	if err := w.Close(); err != nil {
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("disk.CreateCBZ: close zip writer: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("disk.CreateCBZ: fsync: %w", err)
	}

	if err := f.Close(); err != nil {
		removeTmp(tmpPath)
		return fmt.Errorf("disk.CreateCBZ: close file: %w", err)
	}

	return nil
}

// closeAndClean silently closes the zip writer and file, then removes tmpPath.
// Used on error paths where the primary error has already been captured.
func closeAndClean(w *zip.Writer, f *os.File, tmpPath string) {
	_ = w.Close()
	_ = f.Close()
	removeTmp(tmpPath)
}

// writePages adds all page images to the zip writer as uncompressed (Store) entries.
func writePages(w *zip.Writer, pages []fetcher.PageImage) error {
	maxPages := len(pages)
	width := len(fmt.Sprintf("%d", maxPages))
	for i, page := range pages {
		entryName := fmt.Sprintf("%0*d.%s", width, i+1, page.Ext)
		header := &zip.FileHeader{Name: entryName, Method: zip.Store}
		entry, err := w.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("disk.CreateCBZ: create page entry %d: %w", i+1, err)
		}
		if _, err := entry.Write(page.Data); err != nil {
			return fmt.Errorf("disk.CreateCBZ: write page %d: %w", i+1, err)
		}
	}
	return nil
}

// writeComicInfo adds a Deflate-compressed ComicInfo.xml entry to the zip writer.
func writeComicInfo(w *zip.Writer, ci ComicInfo) error {
	xmlData, err := MarshalComicInfo(ci)
	if err != nil {
		return fmt.Errorf("disk.CreateCBZ: marshal ComicInfo: %w", err)
	}
	header := &zip.FileHeader{Name: "ComicInfo.xml", Method: zip.Deflate}
	entry, err := w.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("disk.CreateCBZ: create ComicInfo entry: %w", err)
	}
	if _, err := entry.Write(xmlData); err != nil {
		return fmt.Errorf("disk.CreateCBZ: write ComicInfo: %w", err)
	}
	return nil
}

// ReadComicInfoFromCBZ reads and parses the ComicInfo.xml from inside a CBZ archive.
// Returns nil (with no error) when no ComicInfo.xml entry is present.
func ReadComicInfoFromCBZ(path string) (*ComicInfo, error) {
	// G304: path comes from callers that constructed it from a validated storage root.
	r, err := zip.OpenReader(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("disk.ReadComicInfoFromCBZ: open cbz: %w", err)
	}
	defer func() { _ = r.Close() }()

	for _, f := range r.File {
		if !strings.EqualFold(f.Name, "ComicInfo.xml") {
			continue
		}
		return readComicInfoEntry(f)
	}

	return nil, nil // No ComicInfo.xml present — valid for archives from other tools.
}

// readComicInfoEntry opens and parses a single zip.File entry as ComicInfo XML.
func readComicInfoEntry(f *zip.File) (*ComicInfo, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("disk.ReadComicInfoFromCBZ: open entry: %w", err)
	}
	defer func() { _ = rc.Close() }()

	// G110: ComicInfo.xml is small, structured XML from a controlled internal format.
	// A decompression bomb is not a realistic concern here.
	data, err := io.ReadAll(rc) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("disk.ReadComicInfoFromCBZ: read entry: %w", err)
	}
	return UnmarshalComicInfo(data)
}

// UpdateCBZComicInfo replaces the ComicInfo.xml inside an existing CBZ archive.
//
// All other entries (page images) are preserved verbatim. The replacement is
// atomic: a temporary file is used and renamed over the original on success.
func UpdateCBZComicInfo(cbzPath string, ci ComicInfo) error {
	// G304: cbzPath is constructed from a validated storage root.
	reader, err := zip.OpenReader(cbzPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("disk.UpdateCBZComicInfo: open cbz: %w", err)
	}

	tmpPath := cbzPath + ".tmp"
	f, err := os.Create(tmpPath) //nolint:gosec
	if err != nil {
		_ = reader.Close()
		return fmt.Errorf("disk.UpdateCBZComicInfo: create temp file: %w", err)
	}

	if err := rebuildZip(f, tmpPath, reader, ci); err != nil {
		_ = reader.Close()
		return err
	}
	_ = reader.Close()

	if err := os.Rename(tmpPath, cbzPath); err != nil {
		removeTmp(tmpPath)
		return fmt.Errorf("disk.UpdateCBZComicInfo: rename to final path: %w", err)
	}

	return nil
}

// rebuildZip copies all non-ComicInfo entries from reader into f, appends the new
// ComicInfo, fsyncs, and closes f. On any error, the temp file is removed.
func rebuildZip(f *os.File, tmpPath string, reader *zip.ReadCloser, ci ComicInfo) error {
	w := zip.NewWriter(f)

	for _, entry := range reader.File {
		if strings.EqualFold(entry.Name, "ComicInfo.xml") {
			continue
		}
		if err := copyZipEntry(w, entry); err != nil {
			closeAndClean(w, f, tmpPath)
			return err
		}
	}

	if err := writeComicInfo(w, ci); err != nil {
		closeAndClean(w, f, tmpPath)
		return fmt.Errorf("disk.UpdateCBZComicInfo: %w", err)
	}

	if err := w.Close(); err != nil {
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("disk.UpdateCBZComicInfo: close zip writer: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("disk.UpdateCBZComicInfo: fsync: %w", err)
	}

	if err := f.Close(); err != nil {
		removeTmp(tmpPath)
		return fmt.Errorf("disk.UpdateCBZComicInfo: close file: %w", err)
	}

	return nil
}

// copyZipEntry copies a single zip entry from the source archive into the writer,
// preserving its file header verbatim.
func copyZipEntry(w *zip.Writer, entry *zip.File) error {
	fh := entry.FileHeader
	writer, err := w.CreateHeader(&fh)
	if err != nil {
		return fmt.Errorf("disk.UpdateCBZComicInfo: create entry %q: %w", entry.Name, err)
	}
	rc, err := entry.Open()
	if err != nil {
		return fmt.Errorf("disk.UpdateCBZComicInfo: open entry %q: %w", entry.Name, err)
	}
	defer func() { _ = rc.Close() }()

	// G110: copying existing zip entries from a controlled internal archive.
	if _, err := io.Copy(writer, rc); err != nil { //nolint:gosec
		return fmt.Errorf("disk.UpdateCBZComicInfo: copy entry %q: %w", entry.Name, err)
	}
	return nil
}

// removeTmp silently removes a temporary file. Called on error paths where the
// primary error has already been captured; the cleanup error is intentionally
// discarded.
func removeTmp(path string) {
	_ = os.Remove(path)
}
