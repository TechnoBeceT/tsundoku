package disk

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/technobecet/tsundoku/internal/fetcher"
)

// ErrPageOutOfRange is returned by ReadCBZPage when the requested page index is
// negative or points past the last image entry in the archive. It is a data
// condition (the archive is valid, that page just does not exist), so the HTTP
// reader maps it to a 404 rather than a 400 — the request shape was fine.
var ErrPageOutOfRange = errors.New("disk: cbz page index out of range")

// imagePageExts is the set of file extensions (lowercased, dot-prefixed) that
// ListCBZPages / ReadCBZPage treat as reader pages. Anything else in the archive
// (ComicInfo.xml, stray text/font files, thumbnails with other extensions) is
// ignored so the page index space is exactly the readable images.
var imagePageExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
	".gif":  true,
	".avif": true,
	".bmp":  true,
	".tif":  true,
	".tiff": true,
}

// ListCBZPages returns the archive's image entry names in natural page order
// (so "2.jpg" sorts before "10.jpg", unlike a plain lexical sort), excluding
// ComicInfo.xml, directory entries, and any non-image file. The returned order
// DEFINES the page index space used by ReadCBZPage: index i addresses the i-th
// name here. A missing/corrupt archive yields a wrapped error.
func ListCBZPages(path string) ([]string, error) {
	r, err := zip.OpenReader(path) //nolint:gosec // path built from a validated storage root.
	if err != nil {
		return nil, fmt.Errorf("disk.ListCBZPages: open cbz: %w", err)
	}
	defer func() { _ = r.Close() }()

	entries := sortedImagePageEntries(r.File)
	names := make([]string, len(entries))
	for i, f := range entries {
		names[i] = f.Name
	}
	return names, nil
}

// ReadCBZPage returns the raw bytes and content type of the index-th image page
// in the archive (0-based, in the same natural order as ListCBZPages). The
// content type is resolved from the file extension via mime.TypeByExtension,
// falling back to http.DetectContentType over the decoded bytes when the
// extension is unknown. A negative or too-large index yields ErrPageOutOfRange;
// a missing archive surfaces the underlying fs.ErrNotExist (wrapped); a
// truncated/corrupt entry surfaces a wrapped read error.
func ReadCBZPage(path string, index int) (data []byte, contentType string, err error) {
	r, err := zip.OpenReader(path) //nolint:gosec // path built from a validated storage root.
	if err != nil {
		return nil, "", fmt.Errorf("disk.ReadCBZPage: open cbz: %w", err)
	}
	defer func() { _ = r.Close() }()

	entries := sortedImagePageEntries(r.File)
	if index < 0 || index >= len(entries) {
		return nil, "", ErrPageOutOfRange
	}
	return readImagePageEntry(entries[index])
}

// sortedImagePageEntries filters an archive's file list to reader-page images
// (see isImagePageEntry) and returns them in natural page order. It is the ONE
// place the page set + ordering is defined, so ListCBZPages and ReadCBZPage can
// never disagree on which entry a given index maps to.
func sortedImagePageEntries(files []*zip.File) []*zip.File {
	var imgs []*zip.File
	for _, f := range files {
		if isImagePageEntry(f) {
			imgs = append(imgs, f)
		}
	}
	sort.Slice(imgs, func(i, j int) bool { return naturalLess(imgs[i].Name, imgs[j].Name) })
	return imgs
}

// isImagePageEntry reports whether a zip entry is a reader page: a regular file
// (not a directory), not ComicInfo.xml, with an image extension. The name may
// carry a subdirectory path (some external CBZs nest pages), so the ComicInfo
// and extension checks use the base name.
func isImagePageEntry(f *zip.File) bool {
	if f.FileInfo().IsDir() {
		return false
	}
	base := f.Name
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if strings.EqualFold(base, "ComicInfo.xml") {
		return false
	}
	return imagePageExts[strings.ToLower(filepath.Ext(base))]
}

// readImagePageEntry opens a single zip entry and returns its bytes + resolved
// content type.
func readImagePageEntry(f *zip.File) (data []byte, contentType string, err error) {
	rc, err := f.Open()
	if err != nil {
		// Defensive path: reachable only on a corrupt/truncated archive or fd exhaustion.
		return nil, "", fmt.Errorf("disk.ReadCBZPage: open entry %q: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()

	// G110: page images come from a controlled internal render (or an owner-supplied
	// library CBZ); a decompression bomb is not a realistic concern for a single page.
	data, err = io.ReadAll(rc) //nolint:gosec
	if err != nil {
		// Defensive path: reachable only on corrupt archive data or an I/O error mid-read.
		return nil, "", fmt.Errorf("disk.ReadCBZPage: read entry %q: %w", f.Name, err)
	}
	return data, pageContentType(f.Name, data), nil
}

// pageContentType resolves a page's MIME type from its file extension, falling
// back to sniffing the decoded bytes when the extension is unregistered on the
// host (mime.TypeByExtension is system-tables-dependent, so the byte sniff keeps
// the result deterministic across environments).
func pageContentType(name string, data []byte) string {
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		return ct
	}
	return http.DetectContentType(data)
}

// naturalLess reports whether a sorts before b in natural (human) order: embedded
// digit runs are compared by numeric VALUE, not lexically, so "page2" precedes
// "page10". Non-digit runs are compared byte-wise, case-folded so mixed-case page
// names order stably. Leading zeros are ignored entirely: two runs with equal
// numeric value ("1" vs "001") compare as undecided and scanning continues — the
// order between them is NOT determined by leading-zero width (page enumeration
// never mixes both spellings for the same chapter, so this is immaterial).
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if isASCIIDigit(a[i]) && isASCIIDigit(b[j]) {
			less, decided, ni, nj := compareDigitRuns(a, b, i, j)
			if decided {
				return less
			}
			i, j = ni, nj // equal numeric value — fall through to the next run
			continue
		}
		ca, cb := lowerASCII(a[i]), lowerASCII(b[j])
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	// Shorter remaining tail sorts first (e.g. "page" < "page1").
	return len(a)-i < len(b)-j
}

// compareDigitRuns compares the digit run starting at a[i] against the one at
// b[j] by numeric value, ignoring leading zeros. It returns whether a's run is
// the lesser, whether that decides the order (decided=false means equal value —
// keep scanning), and the indices advanced past both runs.
func compareDigitRuns(a, b string, i, j int) (less, decided bool, ni, nj int) {
	si, sj := i, j
	for i < len(a) && isASCIIDigit(a[i]) {
		i++
	}
	for j < len(b) && isASCIIDigit(b[j]) {
		j++
	}
	na := strings.TrimLeft(a[si:i], "0")
	nb := strings.TrimLeft(b[sj:j], "0")
	if len(na) != len(nb) {
		return len(na) < len(nb), true, i, j
	}
	if na != nb {
		return na < nb, true, i, j
	}
	return false, false, i, j
}

// isASCIIDigit reports whether c is an ASCII digit 0-9.
func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// lowerASCII folds an ASCII byte to lower case (leaving non-letters untouched)
// so naturalLess is case-insensitive without allocating.
func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

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
		// Defensive path: reachable only on OS-level I/O failure (fd exhausted / permission denied).
		return fmt.Errorf("disk.CreateCBZ: create temp file: %w", err)
	}

	if err := writeZipContent(f, tmpPath, pages, ci); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
		return err
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (cross-device rename / permission).
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
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
		closeAndClean(w, f, tmpPath)
		return err
	}

	if err := writeComicInfo(w, ci); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
		closeAndClean(w, f, tmpPath)
		return err
	}

	if err := w.Close(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / corrupt FS).
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("disk.CreateCBZ: close zip writer: %w", err)
	}

	if err := f.Sync(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / corrupt FS).
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("disk.CreateCBZ: fsync: %w", err)
	}

	if err := f.Close(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (fd exhausted / corrupt FS).
		removeTmp(tmpPath)
		return fmt.Errorf("disk.CreateCBZ: close file: %w", err)
	}

	return nil
}

// closeAndClean silently closes the zip writer and file, then removes tmpPath.
// Used on error paths where the primary error has already been captured.
//
// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted /
// rename failure); 0% coverage is expected per engineering standard.
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
			// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
			return fmt.Errorf("disk.CreateCBZ: create page entry %d: %w", i+1, err)
		}
		if _, err := entry.Write(page.Data); err != nil {
			// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
			return fmt.Errorf("disk.CreateCBZ: write page %d: %w", i+1, err)
		}
	}
	return nil
}

// writeComicInfo adds a Deflate-compressed ComicInfo.xml entry to the zip writer.
func writeComicInfo(w *zip.Writer, ci ComicInfo) error {
	xmlData, err := MarshalComicInfo(ci)
	if err != nil {
		// Defensive path: xml.MarshalIndent on a ComicInfo struct (strings + ints only)
		// cannot fail in practice; this guard exists for future schema changes.
		return fmt.Errorf("disk.CreateCBZ: marshal ComicInfo: %w", err)
	}
	header := &zip.FileHeader{Name: "ComicInfo.xml", Method: zip.Deflate}
	entry, err := w.CreateHeader(header)
	if err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
		return fmt.Errorf("disk.CreateCBZ: create ComicInfo entry: %w", err)
	}
	if _, err := entry.Write(xmlData); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
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
		// Defensive path: reachable only on corrupt FS / truncated archive / fd exhausted.
		return nil, fmt.Errorf("disk.ReadComicInfoFromCBZ: open entry: %w", err)
	}
	defer func() { _ = rc.Close() }()

	// G110: ComicInfo.xml is small, structured XML from a controlled internal format.
	// A decompression bomb is not a realistic concern here.
	data, err := io.ReadAll(rc) //nolint:gosec
	if err != nil {
		// Defensive path: reachable only on corrupt archive data or I/O error mid-read.
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
		// Defensive path: reachable only on OS-level I/O failure (fd exhausted / permission denied).
		_ = reader.Close()
		return fmt.Errorf("disk.UpdateCBZComicInfo: create temp file: %w", err)
	}

	if err := rebuildZip(f, tmpPath, reader, ci); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
		_ = reader.Close()
		return err
	}
	_ = reader.Close()

	if err := os.Rename(tmpPath, cbzPath); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (cross-device rename / permission).
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
			// Defensive path: reachable only on OS-level I/O failure (disk full / corrupt archive).
			closeAndClean(w, f, tmpPath)
			return err
		}
	}

	if err := writeComicInfo(w, ci); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
		closeAndClean(w, f, tmpPath)
		return fmt.Errorf("disk.UpdateCBZComicInfo: %w", err)
	}

	if err := w.Close(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / corrupt FS).
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("disk.UpdateCBZComicInfo: close zip writer: %w", err)
	}

	if err := f.Sync(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / corrupt FS).
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("disk.UpdateCBZComicInfo: fsync: %w", err)
	}

	if err := f.Close(); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (fd exhausted / corrupt FS).
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
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted).
		return fmt.Errorf("disk.UpdateCBZComicInfo: create entry %q: %w", entry.Name, err)
	}
	rc, err := entry.Open()
	if err != nil {
		// Defensive path: reachable only on corrupt archive data or fd exhausted.
		return fmt.Errorf("disk.UpdateCBZComicInfo: open entry %q: %w", entry.Name, err)
	}
	defer func() { _ = rc.Close() }()

	// G110: copying existing zip entries from a controlled internal archive.
	if _, err := io.Copy(writer, rc); err != nil { //nolint:gosec
		// Defensive path: reachable only on OS-level I/O failure (disk full / corrupt archive data).
		return fmt.Errorf("disk.UpdateCBZComicInfo: copy entry %q: %w", entry.Name, err)
	}
	return nil
}

// removeTmp silently removes a temporary file. Called on error paths where the
// primary error has already been captured; the cleanup error is intentionally
// discarded.
//
// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted /
// rename failure); 0% coverage is expected per engineering standard.
func removeTmp(path string) {
	_ = os.Remove(path)
}
