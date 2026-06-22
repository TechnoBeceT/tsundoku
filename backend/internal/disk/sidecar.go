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

	// Importance is the provider importance rank (lower = higher priority).
	Importance int `json:"importance"`

	// Filename is the on-disk CBZ filename (basename only, not the full path).
	Filename string `json:"filename"`

	// PageCount is the number of pages in the rendered CBZ.
	PageCount int `json:"page_count"`

	// UploadDate is when the provider published this chapter.
	UploadDate *time.Time `json:"upload_date,omitempty"`
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
	// (index 0 = highest priority). Used by Task 7 to restore ImportanceOrder.
	ProviderOrder []string `json:"provider_order,omitempty"`

	// Chapters is the ordered list of chapter provenance records.
	// New entries are appended; existing entries are updated in-place by chapter_key.
	Chapters []ChapterProvenance `json:"chapters"`
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
		return fmt.Errorf("disk.WriteSidecar: marshal: %w", err)
	}

	finalPath := filepath.Join(dir, sidecarFilename)
	tmpPath := finalPath + ".tmp"

	// G304: tmpPath is constructed from a caller-supplied directory path validated
	// at the storage-root level — not a path traversal concern.
	//nolint:gosec
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("disk.WriteSidecar: create temp file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("disk.WriteSidecar: write: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		removeTmp(tmpPath)
		return fmt.Errorf("disk.WriteSidecar: fsync: %w", err)
	}

	if err := f.Close(); err != nil {
		removeTmp(tmpPath)
		return fmt.Errorf("disk.WriteSidecar: close file: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		removeTmp(tmpPath)
		return fmt.Errorf("disk.WriteSidecar: rename: %w", err)
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
		return nil, fmt.Errorf("disk.ReadSidecar: read file: %w", err)
	}

	var s Sidecar
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("disk.ReadSidecar: unmarshal: %w", err)
	}

	return &s, nil
}
