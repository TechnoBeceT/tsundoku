package disk

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const sidecarFilename = "tsundoku.json"

// ChapterProvenance records the disk and provider metadata for one rendered chapter.
// It is stored in the per-series tsundoku.json and enables Task 7 to reconstruct
// the database without any external index.
type ChapterProvenance struct {
	// ChapterKey is the normalised chapter identity string from Task 1.
	ChapterKey string `json:"chapter_key"`

	// Number is the chapter number; nil for un-numbered chapters.
	Number *float64 `json:"number,omitempty"`

	// Provider is the source provider name (e.g. "mangadex").
	Provider string `json:"provider"`

	// Scanlator is the scanlation group name.
	Scanlator string `json:"scanlator,omitempty"`

	// Importance is the provider importance rank.
	// Tsundoku convention: HIGHER number = HIGHER priority/quality
	// (opposite of legacy Kaizoku.GO where lower was better).
	Importance int `json:"importance"`

	// Filename is the on-disk CBZ filename (basename only, not the full path).
	Filename string `json:"filename"`

	// PageCount is the number of pages in the rendered CBZ.
	PageCount int `json:"page_count"`

	// UploadDate is when the provider published this chapter.
	UploadDate *time.Time `json:"upload_date,omitempty"`
}

// CoverProvenance records the locally cached series cover image.
//
// SourceURL IS the cache key: the stored file is served without ever contacting
// the source again while it still equals the metadata provider's current
// cover_url. A mismatch (the owner switched metadata source, or the source
// changed its thumbnail) is what triggers exactly one re-fetch.
type CoverProvenance struct {
	// File is the on-disk cover filename (basename only, e.g. "cover.jpg").
	File string `json:"file"`

	// SourceURL is the Suwayomi-relative URL the bytes were fetched from.
	SourceURL string `json:"source_url"`

	// Provider is the metadata source the cover came from (identity, not label).
	Provider string `json:"provider,omitempty"`
}

// Sidecar is the per-series tsundoku.json file.
//
// It records series-level metadata, the provider importance order, and the
// provenance of every rendered chapter. The file is written atomically to the
// series directory alongside the CBZ archives.
type Sidecar struct {
	// Title is the series display title.
	Title string `json:"title"`

	// Category is the library category (Manga, Manhwa, etc.).
	Category string `json:"category,omitempty"`

	// ProviderOrder is the ordered list of provider names by importance
	// (index 0 = highest-priority provider; highest importance number — Tsundoku
	// convention: higher importance number = higher priority). Used by Task 7
	// to restore ImportanceOrder.
	ProviderOrder []string `json:"provider_order,omitempty"`

	// Chapters is the ordered list of chapter provenance records.
	// New entries are appended; existing entries are updated in-place by chapter_key.
	Chapters []ChapterProvenance `json:"chapters"`

	// Cover is the locally cached cover image's provenance; nil when the series
	// has no cached cover (every pre-cache series, until its first view).
	Cover *CoverProvenance `json:"cover,omitempty"`
}

// mutateSidecar applies fn to the series' sidecar and writes the result back:
// it reads the existing tsundoku.json (falling back to def when the series has
// none yet), hands the struct to fn, then writes it atomically.
//
// GOTCHA: it does NOT lock. Every caller MUST already hold the per-series-dir
// sidecar lock (lockSidecar in render.go) — the read-modify-write and the fixed
// ".tmp" path are not concurrency-safe, and a chapter render and a cover save
// can hit the same series at the same time.
func mutateSidecar(seriesDir string, def Sidecar, fn func(*Sidecar)) error {
	existing, err := ReadSidecar(seriesDir)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	sidecar := def
	if existing != nil {
		sidecar = *existing
	}

	fn(&sidecar)

	if err := WriteSidecar(seriesDir, sidecar); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted /
		// permission denied) when writing the sidecar JSON.
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// WriteSidecar atomically writes the sidecar to <dir>/tsundoku.json.
//
// The write is atomic: data is written to a temp file alongside the target,
// fsynced, then renamed over the previous file. Errors do not leave a partial
// file at the final path.
func WriteSidecar(dir string, s Sidecar) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("disk.WriteSidecar: create directory: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		// Defensive path: json.MarshalIndent on a Sidecar struct (strings, ints, *time.Time only)
		// cannot fail in practice; this guard exists for future schema changes.
		return fmt.Errorf("disk.WriteSidecar: marshal: %w", err)
	}

	if err := writeFileAtomic(filepath.Join(dir, sidecarFilename), data); err != nil {
		// Defensive path: reachable only on OS-level I/O failure (disk full / fd exhausted /
		// permission denied). writeFileAtomic never leaves a partial file behind.
		return fmt.Errorf("disk.WriteSidecar: %w", err)
	}

	return nil
}

// ReadSidecar reads the tsundoku.json from the given series directory.
// Returns nil (with no error) when no tsundoku.json file exists yet.
func ReadSidecar(dir string) (*Sidecar, error) {
	// G304: path constructed from a caller-supplied directory validated at the
	// storage-root level — not a path traversal concern.
	//nolint:gosec
	data, err := os.ReadFile(filepath.Join(dir, sidecarFilename))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		// Defensive path: reachable only on OS-level I/O failure (permission denied /
		// fd exhausted) after ErrNotExist is already handled above.
		return nil, fmt.Errorf("disk.ReadSidecar: read file: %w", err)
	}

	var s Sidecar
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("disk.ReadSidecar: unmarshal: %w", err)
	}

	return &s, nil
}
