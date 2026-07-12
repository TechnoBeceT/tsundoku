package series

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/pkg/chapterrange"
)

// ErrChapterNotRemovable is returned by RemoveFractionalChapters when a requested
// chapter id is not in the server-recomputed removable set (a whole chapter, a
// fractional a live source still carries, a chapter of another series, or an
// unknown id). The HTTP handler maps it to a 400 — the client's list is a
// SELECTION from the preview, never an authorisation to delete.
var ErrChapterNotRemovable = errors.New("chapter is not removable by fractional cleanup")

// FractionalCleanupChapterDTO is one removable chapter in the cleanup preview:
// the EVIDENCE the owner judges from before ticking it. Number is always
// fractional; PageCount is nullable (nil = the count was never recorded) and is
// the load-bearing field — a 1-page "chapter" is a notice page, a 132-page one is
// a full-size chapter that merely happens to be numbered ".5". Provider is the
// SATISFYING source's display label ("" when the satisfying source was removed).
type FractionalCleanupChapterDTO struct {
	ChapterID string  `json:"chapterId"`
	Number    float64 `json:"number"`
	PageCount *int    `json:"pageCount"`
	Provider  string  `json:"provider"`
	Filename  string  `json:"filename"`
}

// FractionalCleanupDTO is the cleanup preview for one series. TypicalPageCount is
// the MEDIAN page count of the series' WHOLE (non-fractional) downloaded chapters
// — the yardstick that makes "1p" and "132p" legible next to each other; 0 when no
// whole chapter carries a page count. Chapters is always non-nil, so the JSON
// renders [] rather than null.
type FractionalCleanupDTO struct {
	TypicalPageCount int                           `json:"typicalPageCount"`
	Chapters         []FractionalCleanupChapterDTO `json:"chapters"`
}

// FractionalCleanupPreview lists the series' REMOVABLE fractional chapters — the
// already-downloaded fractional CBZs left behind when the owner ticked
// ignore_fractional on a source (the toggle stops NEW fractional downloads and,
// by design, deletes nothing).
//
// A chapter is REMOVABLE when ALL of the following hold:
//
//  1. its number is FRACTIONAL (chapterrange.IsFractional — the ONE definition,
//     shared with the ingest/candidacy gates so this view can never drift from what
//     the engine suppresses);
//  2. it HAS a file (state = downloaded AND filename != "");
//  3. it has at least one carrier, and EVERY SeriesProvider whose feed carries its
//     chapter_key has ignore_fractional = true.
//
// Rule 3 is the RESURRECTION GUARD and the reason the obvious rule ("its SATISFYING
// source is ignored") is wrong: delete a chapter that an ignored source satisfied
// but which a NON-ignored source also carries, and the next refresh sweep re-ingests
// the key, re-creates the Chapter as wanted, and downloads it straight back. "Every
// carrier ignored" makes that structurally impossible — no remaining source can
// re-offer the key.
//
// The "at least one carrier" half of rule 3 is a deliberate STRICTENING of the
// spec's wording (which is vacuously true for a chapter no source carries at all):
// a downloaded fractional with NO carrier cannot be re-downloaded if the owner
// regrets removing it, and no ignored source is even implicated in it, so it is
// never offered. This can only ever offer FEWER chapters, so it cannot weaken the
// resurrection guard.
//
// NO N+1: one series load (chapters + providers + their feeds eager-loaded) — the
// removable set, the provider labels and the median are all resolved in memory.
// An unknown id yields ErrSeriesNotFound.
func (s *Service) FractionalCleanupPreview(ctx context.Context, id uuid.UUID) (FractionalCleanupDTO, error) {
	row, err := s.loadSeriesForCleanup(ctx, id)
	if err != nil {
		return FractionalCleanupDTO{}, err
	}

	providers := providersByID(row.Edges.Providers)
	removable := removableFractionals(row)

	out := make([]FractionalCleanupChapterDTO, 0, len(removable))
	for _, ch := range removable {
		out = append(out, FractionalCleanupChapterDTO{
			ChapterID: ch.ID.String(),
			Number:    *ch.Number,
			PageCount: ch.PageCount,
			Provider:  satisfyingLabel(ch, providers),
			Filename:  ch.Filename,
		})
	}

	return FractionalCleanupDTO{
		TypicalPageCount: typicalPageCount(row),
		Chapters:         out,
	}, nil
}

// RemoveFractionalChapters deletes the selected removable fractional chapters: each
// chapter's CBZ file AND its Chapter row. It returns how many chapters were removed.
//
// 🔴 SANCTIONED OWNER DELETION PATH #5. It is owner-triggered, per-chapter, and
// confirmed in a dialog. NOTHING AUTOMATIC MAY EVER CALL IT — no sweep, no job, no
// side effect of the ignore_fractional toggle (flipping that toggle still deletes
// nothing). The never-auto-delete invariant is intact.
//
// The removable set is RE-COMPUTED SERVER-SIDE (see FractionalCleanupPreview) and
// any id outside it — a whole chapter, a fractional a live source still carries, a
// chapter of another series, an unknown id — is rejected with ErrChapterNotRemovable
// (400) and NOTHING is deleted (all-or-nothing). The client's list is a SELECTION
// from the preview, never an authorisation to delete; without this the endpoint
// would be a general-purpose "delete any chapter" route wearing a cleanup hat.
//
// The ProviderChapter FEED ROWS ARE KEPT. The feed is the SOURCE's offering, not the
// owner's library, and the ingest + candidacy gates already exclude an ignored
// source's fractionals. Keeping the feed is exactly what makes UN-TICKING the toggle
// restore the chapter (it is re-ingested and re-downloaded from the surviving row);
// deleting it would make the toggle a one-way door.
//
// Order (mirrors DeleteSeries): the rows are deleted inside a transaction, the FILES
// are deleted BEFORE the commit, and a file failure rolls the transaction back. So a
// partial failure leaves the DB intact and is retry-safe (the chapter still
// qualifies, a second run finishes the job), and a committed row deletion never
// leaves an orphan CBZ behind for disk.Reconcile to re-import as a disk-origin
// chapter — which would resurrect exactly what was removed.
func (s *Service) RemoveFractionalChapters(ctx context.Context, id uuid.UUID, chapterIDs []uuid.UUID) (int, error) {
	row, err := s.loadSeriesForCleanup(ctx, id)
	if err != nil {
		return 0, err
	}

	targets, err := selectRemovable(row, chapterIDs)
	if err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil
	}

	ids := make([]uuid.UUID, len(targets))
	for i, ch := range targets {
		ids[i] = ch.ID
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("series.RemoveFractionalChapters: begin tx: %w", err)
	}
	if _, err := tx.Chapter.Delete().Where(entchapter.IDIn(ids...)).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("series.RemoveFractionalChapters: delete chapters of series %s: %w", id, err)
	}

	categoryName := category.NameOf(row)
	for _, ch := range targets {
		if _, err := disk.RemoveChapterFile(s.storage, categoryName, row.Title, ch.Filename); err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("series.RemoveFractionalChapters: chapter %s: %w", ch.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("series.RemoveFractionalChapters: commit tx: %w", err)
	}
	return len(targets), nil
}

// loadSeriesForCleanup loads one series with everything the removable rule needs, in
// a single bounded query set: its chapters (number-ASC), its providers WITH their
// availability feeds (the carriers of each chapter_key), and its category (the disk
// folder). An unknown id yields ErrSeriesNotFound.
func (s *Service) loadSeriesForCleanup(ctx context.Context, id uuid.UUID) (*ent.Series, error) {
	row, err := s.client.Series.Query().
		Where(entseries.IDEQ(id)).
		WithChapters(func(cq *ent.ChapterQuery) {
			cq.Order(entchapter.ByNumber(), entchapter.ByChapterKey())
		}).
		WithProviders(func(pq *ent.SeriesProviderQuery) {
			pq.WithProviderChapters()
		}).
		WithCategory().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrSeriesNotFound
		}
		return nil, fmt.Errorf("series: load series %s for fractional cleanup: %w", id, err)
	}
	return row, nil
}

// selectRemovable resolves the caller's chapter ids against the server-recomputed
// removable set. Any id outside the set fails the WHOLE call with
// ErrChapterNotRemovable (all-or-nothing — a mixed list deletes nothing); duplicate
// ids are collapsed so a repeated id cannot inflate the removed count.
func selectRemovable(row *ent.Series, chapterIDs []uuid.UUID) ([]*ent.Chapter, error) {
	removable := make(map[uuid.UUID]*ent.Chapter, len(row.Edges.Chapters))
	for _, ch := range removableFractionals(row) {
		removable[ch.ID] = ch
	}

	targets := make([]*ent.Chapter, 0, len(chapterIDs))
	seen := make(map[uuid.UUID]struct{}, len(chapterIDs))
	for _, cid := range chapterIDs {
		ch, ok := removable[cid]
		if !ok {
			return nil, fmt.Errorf("series: chapter %s: %w", cid, ErrChapterNotRemovable)
		}
		if _, dup := seen[cid]; dup {
			continue
		}
		seen[cid] = struct{}{}
		targets = append(targets, ch)
	}
	return targets, nil
}

// removableFractionals applies the removable rule (see FractionalCleanupPreview) to
// one loaded series and returns the qualifying chapters in the loaded order
// (number-ASC). Purely in-memory over the eager-loaded edges — no query.
func removableFractionals(row *ent.Series) []*ent.Chapter {
	carriers := carriersByKey(row.Edges.Providers)

	out := make([]*ent.Chapter, 0, len(row.Edges.Chapters))
	for _, ch := range row.Edges.Chapters {
		if isRemovableFractional(ch, carriers[ch.ChapterKey]) {
			out = append(out, ch)
		}
	}
	return out
}

// isRemovableFractional is the removable rule for ONE chapter, given every source
// whose feed carries its chapter_key. See FractionalCleanupPreview for the why —
// especially why "every carrier is ignored" (not "its satisfying source is
// ignored") is what makes resurrection impossible.
func isRemovableFractional(ch *ent.Chapter, carriers []*ent.SeriesProvider) bool {
	if ch.State != entchapter.StateDownloaded || ch.Filename == "" {
		return false
	}
	if ch.Number == nil || !chapterrange.IsFractional(*ch.Number) {
		return false
	}
	if len(carriers) == 0 {
		return false
	}
	for _, sp := range carriers {
		if !sp.IgnoreFractional {
			return false
		}
	}
	return true
}

// carriersByKey indexes the series' sources by the chapter keys their feeds CARRY —
// the "who could re-offer this chapter" question the resurrection guard turns on.
// The providers' ProviderChapters edge must be eager-loaded (loadSeriesForCleanup
// does).
func carriersByKey(providers []*ent.SeriesProvider) map[string][]*ent.SeriesProvider {
	byKey := make(map[string][]*ent.SeriesProvider, len(providers))
	for _, sp := range providers {
		for _, pc := range sp.Edges.ProviderChapters {
			byKey[pc.ChapterKey] = append(byKey[pc.ChapterKey], sp)
		}
	}
	return byKey
}

// providersByID indexes a loaded series' providers by their SeriesProvider id, so a
// chapter's satisfying source can be resolved in memory (no per-chapter lookup).
func providersByID(providers []*ent.SeriesProvider) map[uuid.UUID]*ent.SeriesProvider {
	byID := make(map[uuid.UUID]*ent.SeriesProvider, len(providers))
	for _, sp := range providers {
		byID[sp.ID] = sp
	}
	return byID
}

// satisfyingLabel returns the display label of the source that satisfies this
// chapter (the source its file came from), or "" when the chapter has no satisfying
// source — e.g. the owner removed it, which clears satisfied_by but keeps the CBZ.
func satisfyingLabel(ch *ent.Chapter, providers map[uuid.UUID]*ent.SeriesProvider) string {
	if ch.SatisfiedByProviderID == nil {
		return ""
	}
	sp, ok := providers[*ch.SatisfiedByProviderID]
	if !ok {
		return ""
	}
	return ProviderLabel(sp)
}

// typicalPageCount is the MEDIAN page count of the series' WHOLE (non-fractional)
// DOWNLOADED chapters — the yardstick the cleanup dialog renders the fractionals
// against, so "1p" reads as a notice page and "132p" as a full-size chapter that
// merely carries a ".5" number. The median (not the mean) because a single
// 500-page bundle must not drag the yardstick.
//
// Chapters with no recorded page count are skipped; an even-sized sample takes the
// mean of the two middle values. 0 means "no yardstick" (no whole downloaded chapter
// carries a page count) — the UI then shows the raw counts without a comparison.
func typicalPageCount(row *ent.Series) int {
	counts := make([]int, 0, len(row.Edges.Chapters))
	for _, ch := range row.Edges.Chapters {
		if ch.State != entchapter.StateDownloaded || ch.Number == nil || ch.PageCount == nil {
			continue
		}
		if chapterrange.IsFractional(*ch.Number) || *ch.PageCount <= 0 {
			continue
		}
		counts = append(counts, *ch.PageCount)
	}
	if len(counts) == 0 {
		return 0
	}
	slices.Sort(counts)

	mid := len(counts) / 2
	if len(counts)%2 == 1 {
		return counts[mid]
	}
	return (counts[mid-1] + counts[mid]) / 2
}
