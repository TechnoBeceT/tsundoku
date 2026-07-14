package disk

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/technobecet/tsundoku/internal/metadata"
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

	// SourceURL is the absolute (or source-relative) URL the bytes were fetched
	// from — the origin's cover URL, whether that origin is Suwayomi's
	// id-derived thumbnail path or a Phase-1 metadata provider's own cover URL
	// (AniList/MangaDex/…, which flow through this same cache; see
	// SeriesMetadataSidecar.CoverSource for which provider it came from).
	SourceURL string `json:"source_url"`

	// Provider is the metadata source the cover came from (identity, not label).
	Provider string `json:"provider,omitempty"`
}

// SeriesMetadataSidecar is the durable seed for the Phase-1 metadata engine's
// rich series fields — the disk counterpart of the additive jsonb columns on
// Series (internal/ent/schema/series.go: genres/tags/alt_titles/authors/links/
// year/description/status + the metadata_source/cover_source descriptors).
//
// disk.Reconcile reads this block back into those columns (see reconcile.go
// restoreMetadataIndex), so a total DB loss rebuilds the whole rich card from
// disk with ZERO calls to any metadata provider — the same durability
// guarantee CoverProvenance gives the cached cover. Types are reused verbatim
// from internal/metadata (AltTitle/Author/Link/SourceRef) so the sidecar never
// re-declares its own mirror of the pure engine's shapes.
type SeriesMetadataSidecar struct {
	// Description is the merged synopsis (Merge's primary-anchored gap-fill).
	Description string `json:"description,omitempty"`

	// Status is normalized: "ongoing"|"completed"|"hiatus"|"cancelled"|"".
	Status string `json:"status,omitempty"`

	// Genres + Tags are the UNION-merged classification tags across every
	// provider a series was identified against (QCAT-228).
	Genres []string `json:"genres,omitempty"`
	Tags   []string `json:"tags,omitempty"`

	// AltTitles / Authors / Links are the merged collections.
	AltTitles []metadata.AltTitle `json:"alt_titles,omitempty"`
	Authors   []metadata.Author   `json:"authors,omitempty"`
	Links     []metadata.Link     `json:"links,omitempty"`

	// Year is the first-publication year; 0 = unknown.
	Year int `json:"year,omitempty"`

	// MetadataSource is the "anchor-then-aggregate" primary provider the rich
	// fields above were resolved against; nil = not yet identified.
	MetadataSource *metadata.SourceRef `json:"metadata_source,omitempty"`

	// CoverSource is the provider the CURRENTLY CACHED cover's bytes came from
	// — independent of MetadataSource (the cover is chosen separately from the
	// rich-metadata merge, QCAT-228). nil = no cover chosen via this engine yet
	// (the M10 Suwayomi cover proxy predates it, or none cached).
	CoverSource *metadata.SourceRef `json:"cover_source,omitempty"`
}

// TrackBindingSidecar records ONE series↔tracker binding's durable seed —
// which tracker entry a series is bound to, and its last-known progress —
// so disk.Reconcile can restore TrackBinding rows after a total DB loss
// (spec/trackers-oauth-phase3 §3/§5, the tracker subsystem's own extension
// of the same disk-is-truth discipline the Phase-1 metadata block and the
// M1 chapter provenance already give). 🔴 TOKENS ARE NEVER SIDECAR'D: this
// struct carries no credential of any kind — only WHICH tracker entry a
// series is bound to and a last-known-progress snapshot. A DB wipe loses
// the TrackerConnection (account token) entirely; the accepted recovery is
// re-login (LoginCredentials/CompleteOAuth), never a disk-cached secret.
// The binding itself (which entry, its status/progress) survives the wipe
// and re-pulls fresh progress from the tracker on the next FetchTrack.
type TrackBindingSidecar struct {
	// TrackerID is the tracker's numeric registry id (mirrors
	// TrackBinding.tracker_id — MAL=1, AniList=2, Kitsu=3, MangaUpdates=7).
	TrackerID int `json:"tracker_id"`

	// RemoteID is the tracker's manga id this series is bound to.
	RemoteID string `json:"remote_id"`

	// RemoteURL is the canonical link to the remote entry, when known.
	RemoteURL string `json:"remote_url,omitempty"`

	// Status is the tracker's own native status code/string (never
	// normalized here — see TrackBinding.status's own doc comment).
	Status string `json:"status,omitempty"`

	// LastChapterRead is the furthest chapter read as of the last sync —
	// a snapshot, not live; a reconcile restore re-pulls the true current
	// value on the next FetchTrack rather than trusting this as fresh.
	LastChapterRead float64 `json:"last_chapter_read,omitempty"`

	// Score is the reading score on the tracker's native scale.
	Score float64 `json:"score,omitempty"`
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

	// Metadata is the Phase-1 metadata engine's rich-card durable seed; nil
	// when the series has never been auto-identified or manually identified.
	Metadata *SeriesMetadataSidecar `json:"metadata,omitempty"`

	// TrackBindings is the series' current set of tracker bindings (one
	// per tracker the series is bound to); nil/empty when the series has
	// no tracker binding at all. Always a FULL snapshot — a writer
	// overwrites this block wholesale, never merges a single binding into
	// it in place (mirrors WriteMetadata's own full-snapshot contract).
	TrackBindings []TrackBindingSidecar `json:"track_bindings,omitempty"`
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

// WriteMetadata persists the Phase-1 metadata engine's rich-card block into the
// series' tsundoku.json sidecar — the durable seed a total DB loss rebuilds
// from (see disk.Reconcile's restoreMetadataIndex). It reuses mutateSidecar
// under the SAME per-series-dir lock a chapter render or cover save takes
// (lockSidecar in render.go): all three read-modify-write the same file and
// its fixed ".tmp" path, and would otherwise race.
//
// Mirrors SaveCover: it NEVER creates the series directory. A series with
// nothing downloaded yet has no folder, and materialising one just to hold an
// identify result would stage a ghost entry in the Scan-Library wizard (see
// ErrNoSeriesDir) — that series' rich metadata still lives in the DB columns
// (the fast index); only the disk SEED lags until the first chapter lands.
func WriteMetadata(seriesDir string, block SeriesMetadataSidecar) error {
	info, statErr := os.Stat(seriesDir)
	if statErr != nil || !info.IsDir() {
		return fmt.Errorf("disk.WriteMetadata: %w: %q", ErrNoSeriesDir, seriesDir)
	}

	defer lockSidecar(seriesDir)()

	// def is only used when no sidecar exists yet (a series whose folder holds
	// files but never had a render pass through this exact path — defensive;
	// in practice a chapter render or SaveCover always creates the sidecar
	// first). Title/Category are derived from the directory layout itself
	// (<storage>/<Category>/<Title>/) as the best available fallback identity.
	def := Sidecar{Title: filepath.Base(seriesDir), Category: filepath.Base(filepath.Dir(seriesDir))}
	err := mutateSidecar(seriesDir, def, func(s *Sidecar) {
		b := block
		s.Metadata = &b
	})
	if err != nil {
		return fmt.Errorf("disk.WriteMetadata: update sidecar: %w", err)
	}
	return nil
}

// WriteTrackBindings persists the FULL current set of a series' tracker
// bindings into its tsundoku.json sidecar — the durable seed
// disk.Reconcile's restoreTrackBindings rebuilds TrackBinding rows from
// after a total DB loss (spec/trackers-oauth-phase3 §3/§5). Callers pass
// the COMPLETE current list every time (never a partial patch), mirroring
// WriteMetadata's full-snapshot contract: the sidecar block is always
// overwritten wholesale, so a caller that just unbound one tracker and
// re-lists the remainder correctly drops the removed entry from disk too.
//
// Mirrors WriteMetadata/SaveCover: it NEVER creates the series directory
// (see ErrNoSeriesDir) — a series with nothing downloaded yet has no
// folder, and the durable DB row is enough until the first chapter lands;
// the sidecar catches up the first time the series gets one.
func WriteTrackBindings(seriesDir string, bindings []TrackBindingSidecar) error {
	info, statErr := os.Stat(seriesDir)
	if statErr != nil || !info.IsDir() {
		return fmt.Errorf("disk.WriteTrackBindings: %w: %q", ErrNoSeriesDir, seriesDir)
	}

	defer lockSidecar(seriesDir)()

	def := Sidecar{Title: filepath.Base(seriesDir), Category: filepath.Base(filepath.Dir(seriesDir))}
	err := mutateSidecar(seriesDir, def, func(s *Sidecar) {
		s.TrackBindings = bindings
	})
	if err != nil {
		return fmt.Errorf("disk.WriteTrackBindings: update sidecar: %w", err)
	}
	return nil
}

// updateExistingSidecar applies fn to a series' EXISTING sidecar and writes it
// back, taking the per-series-dir lock itself. A series with no sidecar is a
// no-op (there is nothing to rewrite).
//
// It exists so the folder-moving paths (MoveSeriesCategory / RenameCategory)
// rewrite the sidecar through the SAME lock as the render + cover writers: a
// plain cover GET can now write the sidecar at any moment, so an unlocked
// read-modify-write here could silently drop the cover block (or collide on the
// shared ".tmp" path).
func updateExistingSidecar(seriesDir string, fn func(*Sidecar)) error {
	defer lockSidecar(seriesDir)()

	existing, err := ReadSidecar(seriesDir)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if existing == nil {
		return nil
	}

	sidecar := *existing
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
