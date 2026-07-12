package series

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// pageVersionLen mirrors coverVersionLen (cover.go) — 12 hex chars (48 bits) is
// far beyond any collision risk across a personal library.
const pageVersionLen = 12

// PageVersion is the reader page-bytes endpoint's cache-buster: a short hash of
// a chapter's CBZ IDENTITY, derived from fields ALREADY on the Chapter row
// (filename + download_date) — no new schema column, no disk I/O. Both fields
// change whenever the CBZ is (re)written: a fresh download sets both
// (download/dispatcher.go finishDownload), and so does a Library-Convergence
// upgrade that re-renders the chapter from a better source
// (download/upgrade.go). So a replaced CBZ always earns a new version, and a
// page URL built from a superseded version can never be mistaken for current.
//
// GOTCHA — unlike series.coverVersion (which hashes the actual cover BYTES),
// this is a PROXY for the bytes, not a hash of them: it can drift if the owner
// replaces a CBZ file out of band without going through download/upgrade. That
// gap is exactly why the page endpoint must never answer `immutable` off this
// version — see handler/series/reader.go's Cache-Control doc comment. A bounded
// max-age self-heals a drifted version; `immutable` would not.
//
// A not-yet-downloaded chapter (empty filename) or one with no recorded
// download_date has nothing to version and returns "".
func PageVersion(filename string, downloadDate *time.Time) string {
	if filename == "" || downloadDate == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(filename + "|" + downloadDate.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])[:pageVersionLen]
}

// ErrChapterNotFound is returned by the reader methods when no chapter matches
// the requested id (or, for ChapterPage, does not belong to the given series).
// The HTTP handler maps it to a 404.
var ErrChapterNotFound = errors.New("chapter not found")

// ErrChapterFileMissing is returned by ChapterPage when the chapter has no
// rendered CBZ to serve: it has no filename recorded (never downloaded) or the
// file is absent on disk. There is nothing to stream, so the HTTP handler maps
// it to a 404 — NOT a 502, because the archive simply isn't there rather than
// failing to read.
var ErrChapterFileMissing = errors.New("chapter file missing")

// ErrPageRead is returned by ChapterPage when the CBZ exists but a page could
// not be decoded (a truncated/corrupt archive or an I/O error mid-read). This is
// a genuine failure to serve data that should be there, so the HTTP handler maps
// it to a 502.
var ErrPageRead = errors.New("chapter page read failed")

// ChapterPageVersion returns the reader page-bytes endpoint's cache-buster for
// a chapter's CURRENT CBZ (see PageVersion) — a single narrow column read,
// mirroring series.CoverVersion. It is the CHEAP half of the page-bytes
// endpoint: a conditional request (If-None-Match) is answered from this alone,
// so a 304 NEVER opens the archive. The chapter must belong to seriesID (a
// mismatch is ErrChapterNotFound, matching ChapterPage). Returns "" for a
// chapter with no rendered CBZ yet (nothing to version).
func (s *Service) ChapterPageVersion(ctx context.Context, seriesID, chapterID uuid.UUID) (string, error) {
	ch, err := s.client.Chapter.Query().
		Where(entchapter.IDEQ(chapterID), entchapter.SeriesID(seriesID)).
		Select(entchapter.FieldFilename, entchapter.FieldDownloadDate).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", ErrChapterNotFound
		}
		return "", fmt.Errorf("series.ChapterPageVersion: load chapter %s: %w", chapterID, err)
	}
	return PageVersion(ch.Filename, ch.DownloadDate), nil
}

// ChapterPage returns the raw bytes and content type of the n-th page (0-based)
// of a chapter's rendered CBZ. The chapter must belong to seriesID (a mismatch
// is ErrChapterNotFound, so one series can never read another's files). The CBZ
// path is resolved from the disk layout contract via disk.ChapterCBZPath, then
// the page is read with disk.ReadCBZPage.
//
// Errors: ErrChapterNotFound (unknown chapter / wrong series) → 404;
// ErrChapterFileMissing (no filename or file absent) → 404;
// disk.ErrPageOutOfRange (page index past the archive) → 404; ErrPageRead (CBZ
// present but undecodable) → 502.
func (s *Service) ChapterPage(ctx context.Context, seriesID, chapterID uuid.UUID, n int) (data []byte, contentType string, err error) {
	ch, err := s.client.Chapter.Query().
		Where(entchapter.IDEQ(chapterID), entchapter.SeriesID(seriesID)).
		WithSeries(func(sq *ent.SeriesQuery) { sq.WithCategory() }).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, "", ErrChapterNotFound
		}
		return nil, "", fmt.Errorf("series.ChapterPage: load chapter %s: %w", chapterID, err)
	}
	if ch.Filename == "" {
		return nil, "", ErrChapterFileMissing
	}

	row := ch.Edges.Series
	path := disk.ChapterCBZPath(s.storage, category.NameOf(row), row.Title, ch.Filename)

	data, contentType, err = disk.ReadCBZPage(path, n)
	if err != nil {
		switch {
		case errors.Is(err, disk.ErrPageOutOfRange):
			return nil, "", err // passthrough — handler maps to 404
		case errors.Is(err, fs.ErrNotExist):
			// The DB says the chapter is downloaded but the file is gone (moved,
			// deleted, or a drifted title) — a 404, not a read failure.
			return nil, "", ErrChapterFileMissing
		default:
			return nil, "", fmt.Errorf("series.ChapterPage: %w: %w", ErrPageRead, err)
		}
	}
	return data, contentType, nil
}

// ChapterProgressDTO is the reading-progress subset returned by SetProgress: the
// chapter's id plus its persisted progress. ReadAt is nil until the chapter is
// first marked read (and is cleared when it is un-marked).
type ChapterProgressDTO struct {
	ID           string     `json:"id"`
	Read         bool       `json:"read"`
	LastReadPage int        `json:"lastReadPage"`
	ReadAt       *time.Time `json:"readAt"`
}

// SetProgress records the owner's reading progress for one chapter: its last-read
// page and whether it is fully read. read_at is stamped to now when read is true
// and cleared when false, so it always means "when this chapter was marked read".
// Progress is pure owner UI state — no disk/sidecar effect, never
// download-determining. A missing chapter yields ErrChapterNotFound. Returns the
// updated subset so the caller confirms the new state without a refetch (§16).
func (s *Service) SetProgress(ctx context.Context, chapterID uuid.UUID, lastReadPage int, read bool) (ChapterProgressDTO, error) {
	upd := s.client.Chapter.UpdateOneID(chapterID).
		SetRead(read).
		SetLastReadPage(lastReadPage)
	if read {
		upd = upd.SetReadAt(time.Now().UTC())
	} else {
		upd = upd.ClearReadAt()
	}

	ch, err := upd.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ChapterProgressDTO{}, ErrChapterNotFound
		}
		return ChapterProgressDTO{}, fmt.Errorf("series.SetProgress: update chapter %s: %w", chapterID, err)
	}
	return ChapterProgressDTO{
		ID:           ch.ID.String(),
		Read:         ch.Read,
		LastReadPage: ch.LastReadPage,
		ReadAt:       ch.ReadAt,
	}, nil
}
