package disk

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// coverBasename is the stem of the cached cover file. The full name is
// "<coverBasename>.<ext>" (e.g. "cover.jpg") — the Komga-adopting name, so Komga
// uses the real source cover as the series poster instead of deriving one from
// the first CBZ page. It is safe to sit next to the CBZs: the dedup sweeps and
// the library scanner both only ever consider ".cbz" files.
const coverBasename = "cover"

// defaultCoverExt is used when the upstream reports no usable image extension —
// an extensionless (or dot-prefixed) cover file would be invisible to Komga.
const defaultCoverExt = "jpg"

// ErrNoLocalCover reports that the series has no usable cached cover: no sidecar,
// no cover block, an unreadable sidecar, or a cover file that has since vanished.
// It is never an I/O failure — every "cannot use the cache" condition collapses
// to this one sentinel so the caller's only response is "fetch it once".
var ErrNoLocalCover = errors.New("no local cover")

// CoverRequest carries everything SaveCover needs to persist one series cover.
type CoverRequest struct {
	// Storage is the root library directory (e.g. "/data/library").
	Storage string

	// Category is the series' library category (its folder under Storage).
	Category string

	// Title is the series title (its folder under the category).
	Title string

	// Data is the raw image, exactly as the source served it (never resized).
	Data []byte

	// Ext is the image extension reported by Suwayomi ("jpg", "png", "webp", …).
	// An empty or unusable value degrades to defaultCoverExt.
	Ext string

	// SourceURL is the URL the bytes were fetched from — the cache key.
	SourceURL string

	// Provider is the metadata source the cover came from (identity, not label).
	Provider string
}

// SaveCover writes the series cover into the series folder and records its
// provenance in the tsundoku.json sidecar, returning the on-disk filename.
//
// The image is written atomically (temp → fsync → rename), so a failed write
// never leaves a partial cover at the final path. Any previously cached cover is
// replaced — including one stored under a different extension (a metadata-source
// switch can change the image type), so a stale cover.* can never linger and win.
//
// The whole operation takes the per-series-dir sidecar lock: a chapter render and
// a cover save both read-modify-write the same tsundoku.json (and its fixed
// ".tmp" path), and would otherwise drop one of the two blocks.
func SaveCover(req CoverRequest) (filename string, err error) {
	seriesDir := SeriesDir(req.Storage, req.Category, req.Title)
	if mkErr := os.MkdirAll(seriesDir, 0o750); mkErr != nil {
		return "", fmt.Errorf("disk.SaveCover: create series dir: %w", mkErr)
	}

	filename = coverBasename + "." + normalizeCoverExt(req.Ext)

	defer lockSidecar(seriesDir)()

	if err := writeFileAtomic(filepath.Join(seriesDir, filename), req.Data); err != nil {
		return "", fmt.Errorf("disk.SaveCover: %w", err)
	}
	removeStaleCovers(seriesDir, filename)

	def := Sidecar{Title: req.Title, Category: req.Category}
	err = mutateSidecar(seriesDir, def, func(s *Sidecar) {
		s.Cover = &CoverProvenance{
			File:      filename,
			SourceURL: req.SourceURL,
			Provider:  req.Provider,
		}
	})
	if err != nil {
		return "", fmt.Errorf("disk.SaveCover: update sidecar: %w", err)
	}

	return filename, nil
}

// ReadCover returns the cached cover bytes, its bare extension, and the sidecar
// provenance that says which source URL those bytes came from.
//
// Every "no usable cache" condition — no sidecar, no cover block, a corrupt
// sidecar, a cover file that was deleted underneath us — returns ErrNoLocalCover
// and nothing else: the cache is advisory, so it must never turn a servable page
// into an error. The caller re-fetches on that sentinel.
func ReadCover(storage, category, title string) (data []byte, ext string, prov *CoverProvenance, err error) {
	seriesDir := SeriesDir(storage, category, title)

	sidecar, readErr := ReadSidecar(seriesDir)
	if readErr != nil || sidecar == nil || sidecar.Cover == nil || sidecar.Cover.File == "" {
		return nil, "", nil, ErrNoLocalCover
	}

	// filepath.Base pins the file to the series dir — the sidecar is on-disk data,
	// so a hand-edited "../../etc/passwd" must not escape the library root.
	name := filepath.Base(sidecar.Cover.File)

	// G304: path is the storage root + sanitised folder names + a basename.
	//nolint:gosec
	data, readErr = os.ReadFile(filepath.Join(seriesDir, name))
	if readErr != nil {
		return nil, "", nil, ErrNoLocalCover
	}

	cover := *sidecar.Cover
	return data, strings.TrimPrefix(filepath.Ext(name), "."), &cover, nil
}

// normalizeCoverExt reduces the upstream-reported extension to a safe, bare,
// lowercase extension. Anything unusable (empty, or carrying separators/dots
// that could escape the series dir) degrades to defaultCoverExt rather than
// producing a dotfile, an extensionless cover, or a traversal.
func normalizeCoverExt(ext string) string {
	clean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	if clean == "" {
		return defaultCoverExt
	}
	for _, r := range clean {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return defaultCoverExt
		}
	}
	return clean
}

// removeStaleCovers deletes every cover.* file in the series dir except keep.
// Only the cover the sidecar points at may survive — an orphaned cover.png next
// to a fresh cover.jpg would be picked up by Komga and shown instead. It touches
// nothing but "cover.*" (never a CBZ), and a failed unlink is ignored: a stale
// cache file is not worth failing the request over.
func removeStaleCovers(seriesDir, keep string) {
	matches, err := filepath.Glob(filepath.Join(seriesDir, coverBasename+".*"))
	if err != nil {
		// Defensive path: Glob only errors on a malformed pattern, and this pattern is a constant.
		return
	}
	for _, m := range matches {
		if filepath.Base(m) == keep {
			continue
		}
		_ = os.Remove(m)
	}
}
