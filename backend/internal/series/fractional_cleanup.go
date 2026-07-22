package series

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/chapter"
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

// ErrFractionalCleanupFailed is returned by RemoveFractionalChapters when a CBZ
// deletion fails partway. The transaction is rolled back, so NO chapter row is
// removed — but the files deleted BEFORE the failure are already gone, and the
// returned count (0) understates that. The call is retry-safe: every selected
// chapter still qualifies (its row survived), and re-running the cleanup finishes
// the job — an already-deleted file is a no-op. The handler maps this to a 500
// whose message says exactly that.
var ErrFractionalCleanupFailed = errors.New("fractional cleanup failed while deleting files")

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
	row, err := loadSeriesForCleanup(ctx, s.client, id)
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
// are deleted BEFORE the commit, and a file failure rolls the transaction back — so a
// committed row deletion never leaves an orphan CBZ behind for disk.Reconcile to
// re-import as a disk-origin chapter, which would resurrect exactly what was removed.
//
// A partial failure leaves the DB — not the disk — intact: the transaction is rolled
// back so NO chapter row is removed, but the CBZs deleted before the failing one are
// already gone, and the returned count (0, with ErrFractionalCleanupFailed) understates
// that. That asymmetry is deliberate and retry-safe: every selected chapter still
// qualifies (its row survived), so re-running the cleanup finishes the job and an
// already-deleted file is a no-op.
//
// TOCTOU: the removable set is recomputed from a load taken INSIDE the transaction and
// the DELETE re-asserts the rule's row-level half shared by every caller of
// deleteRemovableTargets (this series, still downloaded, still has a filename) as SQL
// predicates, so a concurrent change cannot slip a now-protected chapter past the
// check. Number-ness is NOT one of those SQL predicates — see the comment on the
// DELETE itself for why — but it is guaranteed at selection time here: every chapter
// in this call's removable set passed isDownloadedFractional, which requires a
// non-nil fractional Number. A mismatch between the selected and the deleted count
// fails the whole call (ErrChapterNotRemovable) — nothing is deleted.
func (s *Service) RemoveFractionalChapters(ctx context.Context, id uuid.UUID, chapterIDs []uuid.UUID) (int, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("series.RemoveFractionalChapters: begin tx: %w", err)
	}

	removed, err := s.removeFractionalInTx(ctx, tx, id, chapterIDs)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("series.RemoveFractionalChapters: commit tx: %w", err)
	}
	return removed, nil
}

// removeFractionalInTx is the body of RemoveFractionalChapters, run inside one
// transaction: the series (chapters + providers + feeds) is loaded through the TX
// client, so the removable rule is decided and enforced on ONE snapshot — the owner
// un-ticking ignore_fractional in another tab mid-request can no longer make the check
// and the delete disagree. Every error rolls the caller's transaction back.
func (s *Service) removeFractionalInTx(ctx context.Context, tx *ent.Tx, id uuid.UUID, chapterIDs []uuid.UUID) (int, error) {
	row, err := loadSeriesForCleanup(ctx, tx.Client(), id)
	if err != nil {
		return 0, err
	}

	targets, err := selectRemovable(row, chapterIDs)
	if err != nil {
		return 0, err
	}
	return s.deleteRemovableTargets(ctx, tx, id, row, targets)
}

// deleteRemovableTargets deletes a resolved set of removable fractional chapters
// (rows + CBZs) inside the caller's transaction: rows first (behind belt-and-braces
// SQL predicates), then the files BEFORE the caller commits so a file failure rolls
// the rows back. Shared by the selection path (RemoveFractionalChapters) and the
// whole-series sweep (DedupeFiles' ignored-downloaded-fractional pass) so the
// files-before-commit ordering and the delete guard live in exactly one place.
func (s *Service) deleteRemovableTargets(ctx context.Context, tx *ent.Tx, id uuid.UUID, row *ent.Series, targets []*ent.Chapter) (int, error) {
	if len(targets) == 0 {
		return 0, nil
	}

	ids := make([]uuid.UUID, len(targets))
	for i, ch := range targets {
		ids[i] = ch.ID
	}

	// Belt and braces: the in-tx snapshot makes the decision correct, these predicates
	// make the DELETE itself un-bypassable — it can only ever touch a downloaded,
	// filed chapter OF THIS SERIES, whatever the caller sent. This is shared by TWO
	// callers (RemoveFractionalChapters and RemoveSourcelessChapters) so the guard
	// only asserts what BOTH rules require. Number-ness is NOT asserted here on
	// purpose: for the fractional rule it is a decision-layer guarantee
	// (isDownloadedFractional requires ch.Number != nil before a chapter ever reaches
	// this set), so re-asserting it in SQL would be redundant, not protective — but
	// for the sourceless rule a target may legitimately have a nil Number (a
	// sourceless chapter can lack a parsed number, see SourcelessCleanupChapterDTO),
	// so an entchapter.NumberNotNil() predicate here would wrongly exclude it from the
	// DELETE, making deleted != len(targets) and failing the WHOLE call with
	// ErrChapterNotRemovable even though the preview just offered it. Do not
	// reintroduce it. (The remaining rule halves — fractional-ness and "every carrier
	// ignored" for the fractional path, "zero carriers" for the sourceless path — are
	// not expressible as Ent predicates either way; they are enforced by
	// removableFractionals / removableSourceless above.)
	deleted, err := tx.Chapter.Delete().Where(
		entchapter.IDIn(ids...),
		entchapter.SeriesID(id),
		entchapter.StateEQ(entchapter.StateDownloaded),
		entchapter.FilenameNEQ(""),
	).Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("series: delete removable fractional chapters of series %s: %w", id, err)
	}
	if deleted != len(targets) {
		return 0, fmt.Errorf("series: %d of %d removable fractional chapters no longer matched the removable rule at delete time, nothing removed: %w",
			len(targets)-deleted, len(targets), ErrChapterNotRemovable)
	}

	if err := s.removeCleanupFiles(row, targets); err != nil {
		return 0, err
	}
	return len(targets), nil
}

// removeCleanupFiles deletes the selected chapters' CBZs from the series folder. It
// runs BEFORE the commit: a failure rolls the row deletion back, so no committed
// deletion can leave an orphan CBZ for disk.Reconcile to re-import.
//
// 🔴 A file that is NOT THERE is logged, not failed. disk.RemoveChapterFile reports
// removed=false when the CBZ (or the whole series folder) is not where the DB says it
// is — the on-disk title/category can drift from the DB (series.DeleteSeries warns on
// exactly the same condition). Swallowing that silently would be the resurrection bug
// through a side door: the row deletion commits, the real CBZ survives under the
// drifted name, and disk.Reconcile re-imports it as a disk-origin chapter. Hard-failing
// instead is worse — it would make a retry (whose earlier files ARE already gone)
// permanently unable to complete. So the honest signal is a WARN naming the series, the
// chapter number and the filename we expected to find.
func (s *Service) removeCleanupFiles(row *ent.Series, targets []*ent.Chapter) error {
	categoryName := category.NameOf(row)
	for _, ch := range targets {
		removed, err := disk.RemoveChapterFile(s.storage, categoryName, row.Title, ch.Filename)
		if err != nil {
			return fmt.Errorf("%w: series %s chapter %s (%q): %w", ErrFractionalCleanupFailed, row.ID, ch.ID, ch.Filename, err)
		}
		if !removed {
			// ch.Number is dereferenced through FormatChapterNumber (never a raw
			// pointer in the log line) — every target has a number by the rule.
			slog.Warn("series.RemoveFractionalChapters: no CBZ found for the chapter — nothing deleted on disk (the on-disk title/category may have drifted from the DB, leaving the real file behind for a reconcile to re-import)",
				"series_id", row.ID, "title", row.Title, "category", categoryName,
				"chapter_id", ch.ID, "number", chapterNumberLabel(ch), "filename", ch.Filename)
		}
	}
	return nil
}

// chapterNumberLabel renders a chapter's (nullable) number for a log line — the same
// formatting the chapter keys and CBZ filenames use, "?" when it is absent.
func chapterNumberLabel(ch *ent.Chapter) string {
	if ch.Number == nil {
		return "?"
	}
	return chapter.FormatChapterNumber(*ch.Number)
}

// loadSeriesForCleanup loads one series with everything the removable rule needs, in
// a single bounded query set: its chapters (number-ASC), its providers WITH their
// availability feeds (the carriers of each chapter_key), and its category (the disk
// folder). An unknown id yields ErrSeriesNotFound.
//
// The client is a parameter (not s.client) precisely so the REMOVAL path can pass the
// TRANSACTION's client — the check and the delete then share one snapshot.
func loadSeriesForCleanup(ctx context.Context, client *ent.Client, id uuid.UUID) (*ent.Series, error) {
	row, err := client.Series.Query().
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

// hasDownloadedFile reports whether a chapter has a real CBZ on disk: it is in the
// downloaded state AND carries a filename. The ONE definition of "there is a file
// here" shared by every cleanup rule (fractional and sourceless) so they can never
// drift on what a deletable, on-disk chapter is (§2 DRY).
func hasDownloadedFile(ch *ent.Chapter) bool {
	return ch.State == entchapter.StateDownloaded && ch.Filename != ""
}

// isDownloadedFractional reports whether a chapter is a DOWNLOADED fractional
// with a file on disk — the IGNORE-AGNOSTIC predicate ("is there a fractional CBZ
// here at all"). It is the first half of the removable rule, extracted so the two
// callers that ask this question — isRemovableFractional (the strict removable
// set) and downloadedFractionalCount (the library-wide list tally) — can never
// drift on what "a downloaded fractional" means (§2 DRY).
func isDownloadedFractional(ch *ent.Chapter) bool {
	if !hasDownloadedFile(ch) {
		return false
	}
	return ch.Number != nil && chapterrange.IsFractional(*ch.Number)
}

// downloadedFractionalCount counts a series' DOWNLOADED fractional chapters,
// IGNORE-AGNOSTIC — every fractional CBZ on disk regardless of any source's
// ignore_fractional flag. It is the library Fractionals page's LIST CRITERION (a
// series is listed when this is > 0) and the row's total; the broader superset of
// removableFractionals (which additionally requires ≥1 carrier and EVERY carrier
// ignored). Purely in-memory over the eager-loaded chapters — no query.
func downloadedFractionalCount(row *ent.Series) int {
	n := 0
	for _, ch := range row.Edges.Chapters {
		if isDownloadedFractional(ch) {
			n++
		}
	}
	return n
}

// isRemovableFractional is the removable rule for ONE chapter, given every source
// whose feed carries its chapter_key. See FractionalCleanupPreview for the why —
// especially why "every carrier is ignored" (not "its satisfying source is
// ignored") is what makes resurrection impossible.
//
// 🔴 The len(carriers) == 0 guard is LOAD-BEARING, not a formality — do not
// "simplify" it away (TestFractionalCleanupPreview_ZeroCarriersNeverRemovable pins
// it): a downloaded fractional that NO source carries is irreplaceable (nothing can
// re-download it) and no ignored source is even implicated in it, so this endpoint
// never offers it.
//
// The CONSEQUENCE, stated plainly because it is a real trade-off: RemoveProvider
// deletes the source's whole ProviderChapter feed, so once the owner REMOVES an
// ignored source, its already-downloaded fractionals lose their last carrier and
// become permanently un-cleanable HERE. To clean them he must re-add the source and
// re-tick ignore_fractional (restoring the carrier), or delete the series outright
// (DeleteSeries). The alternative — offering carrier-less fractionals — would let
// this endpoint delete files nothing can ever restore, which is a worse trade.
func isRemovableFractional(ch *ent.Chapter, carriers []*ent.SeriesProvider) bool {
	if !isDownloadedFractional(ch) {
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
