package series

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/pkg/chapterrange"
)

// ErrDedupeMergeFailed is returned by DedupeFiles when a merged duplicate's CBZ
// deletion fails partway through the engine-switch merge pass. The transaction is
// rolled back, so NO chapter row is removed — but the CBZs deleted before the
// failing one are already gone. The call is retry-safe: every surviving duplicate
// still qualifies (its rows were restored by the rollback), so re-running the
// sweep finishes the job and an already-deleted file is a no-op. Falls through the
// handler to a 500.
var ErrDedupeMergeFailed = errors.New("dedupe-files failed while deleting a merged duplicate's CBZ")

// DedupeFiles is the owner-triggered duplicate cleanup for a whole series. It runs
// FOUR passes and returns the total number of duplicates/leftovers it resolved:
//
//  0. Engine-switch duplicate CHAPTER-ROW merge (see mergeEngineSwitchDuplicates):
//     the Suwayomi→Rensaio switch keyed the SAME physical chapter twice — once as a
//     negative-numeric literal ("-1", from Suwayomi's "number = -1" epilogue) and
//     once name-keyed ("name:epilogue", from Rensaio reporting it unparseable) — so
//     UNIQUE(series_id, chapter_key) never deduped them and BOTH the Chapter row and
//     its CBZ persist twice. This pass merges each PROVABLE pair (matched by the
//     source chapter URL on ProviderChapter, the true identity across both ingests),
//     keeping the name-keyed canonical and deleting the negative-numeric legacy row +
//     its orphaned feed rows + its CBZ. Runs in its own transaction with
//     files-deleted-before-commit + rollback.
//     0b. Ignored DOWNLOADED fractional cleanup (see removeIgnoredDownloadedFractionals):
//     every fractional chapter that is DOWNLOADED and whose EVERY carrier now
//     ignores fractionals — i.e. was downloaded BEFORE the owner ticked
//     ignore_fractional on its source(s) — is deleted (row + CBZ), the whole-series
//     counterpart of the per-chapter RemoveFractionalChapters. It reuses that
//     endpoint's exact removable rule (fractional AND >=1 carrier AND every carrier
//     ignored — the resurrection guard) and its files-before-commit ordering. Like
//     pass 0 it deletes Chapter ROWs, in its own transaction, and like every
//     deletion path it is owner-triggered, never automatic.
//  1. For every downloaded chapter (one with a winning filename AND a number) it
//     removes any OTHER CBZ in the series folder that shares that chapter's number,
//     keeping the chapter's own winning filename.
//  2. For every SUPERSEDED fractional-part chapter (fractional-part suppression
//     clears its DB filename and best-effort removes its CBZ; if that removal ever
//     failed the CBZ is orphaned on disk and invisible to pass 1) it removes EVERY
//     .cbz matching that fractional number, with NO keeper.
//
// Passes 1–2 are the bulk counterpart to the automatic per-convergence cleanup in
// the upgrade path: they reconcile a library that accumulated duplicate CBZs before
// the convergence engine existed (e.g. an imported Kaizoku library). They perform NO
// DB writes — only orphan/duplicate on-disk files are removed — and NEVER delete a
// chapter's winning file. A missing series folder yields 0 (nothing to sweep). An
// unknown id returns ErrSeriesNotFound (mapped to 404 by the handler).
//
// 🔴 OWNER-TRIGGERED ONLY. Nothing automatic calls DedupeFiles — no boot step, no
// reconcile, no refresh sweep, no download/upgrade cycle. It runs solely from the
// owner's explicit POST /api/series/:id/dedupe-files ("Remove duplicate files"
// button). The never-auto-delete invariant is intact.
func (s *Service) DedupeFiles(ctx context.Context, id uuid.UUID) (int, error) {
	// DB-mutating passes first (each in its own tx, delete rows): the file-only sweep
	// then loads fresh so it sees the post-deletion state (no stale removed chapter).
	merged, err := s.mergeEngineSwitchDuplicates(ctx, id)
	if err != nil {
		return merged, err
	}
	ignoredRemoved, err := s.removeIgnoredDownloadedFractionals(ctx, id)
	dbRemoved := merged + ignoredRemoved
	if err != nil {
		return dbRemoved, err
	}

	ser, err := s.client.Series.Query().
		Where(entseries.IDEQ(id)).
		WithCategory().
		WithChapters().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return dbRemoved, ErrSeriesNotFound
		}
		return dbRemoved, fmt.Errorf("series.DedupeFiles: load series %s: %w", id, err)
	}

	categoryName := category.NameOf(ser)
	total := dbRemoved

	winningRemoved, err := dedupeWinningChapters(s.storage, categoryName, ser)
	total += winningRemoved
	if err != nil {
		return total, err
	}

	supersededRemoved, err := dedupeSupersededPartOrphans(s.storage, categoryName, ser)
	total += supersededRemoved
	if err != nil {
		return total, err
	}

	return total, nil
}

// dedupeWinningChapters is DedupeFiles pass 1: for every chapter with a
// winning filename AND a number, remove any OTHER CBZ in the series folder
// sharing that number, keeping the winner. Returns the count removed.
func dedupeWinningChapters(storage, categoryName string, ser *ent.Series) (int, error) {
	total := 0
	for _, ch := range ser.Edges.Chapters {
		if ch.Filename == "" || ch.Number == nil {
			continue
		}
		removed, err := disk.RemoveOtherChapterFiles(
			storage, categoryName, ser.Title,
			chapter.FormatChapterNumber(*ch.Number), ch.Filename,
		)
		if err != nil {
			return total, fmt.Errorf("series.DedupeFiles: chapter %s: %w", ch.ID, err)
		}
		total += removed
	}
	return total, nil
}

// dedupeSupersededPartOrphans is DedupeFiles pass 2 (see the DedupeFiles doc
// comment): for every superseded FRACTIONAL-part chapter, remove EVERY CBZ
// matching its number with no keeper. Whole-integer superseded chapters are
// skipped. Returns the count removed.
func dedupeSupersededPartOrphans(storage, categoryName string, ser *ent.Series) (int, error) {
	total := 0
	for _, ch := range ser.Edges.Chapters {
		if ch.State != entchapter.StateSuperseded || ch.Number == nil {
			continue
		}
		n := *ch.Number
		if !chapterrange.IsFractional(n) {
			// Whole-integer "superseded" chapter: not a split part, skip —
			// its file (if any) is a legitimate keeper elsewhere.
			continue
		}
		removed, err := disk.RemoveOtherChapterFiles(
			storage, categoryName, ser.Title,
			chapter.FormatChapterNumber(n), "",
		)
		if err != nil {
			return total, fmt.Errorf("series.DedupeFiles: superseded chapter %s: %w", ch.ID, err)
		}
		total += removed
	}
	return total, nil
}

// duplicatePair is one engine-switch duplicate: keep is the name-keyed canonical
// chapter (the go-forward Rensaio key), remove is its negative-numeric legacy twin
// (the Suwayomi artifact). Both proven to be the SAME physical chapter by a shared
// source chapter URL — see detectEngineSwitchDuplicates.
type duplicatePair struct {
	keep   *ent.Chapter
	remove *ent.Chapter
}

// mergeEngineSwitchDuplicates is DedupeFiles pass 0: it merges every provable
// engine-switch duplicate chapter pair in one transaction and returns how many
// duplicates were merged. All DB row changes happen inside the transaction and the
// CBZ deletions run BEFORE the commit, so a disk failure rolls the row changes back
// — a committed merge can never leave an orphan CBZ behind for disk.Reconcile to
// re-import (which would resurrect exactly what was removed).
func (s *Service) mergeEngineSwitchDuplicates(ctx context.Context, id uuid.UUID) (int, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("series.DedupeFiles: begin merge tx: %w", err)
	}

	merged, err := s.mergeDuplicatesInTx(ctx, tx, id)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("series.DedupeFiles: commit merge tx: %w", err)
	}
	return merged, nil
}

// removeIgnoredDownloadedFractionals is DedupeFiles pass 0b: it deletes every
// DOWNLOADED fractional chapter of the series whose EVERY carrier ignores
// fractionals (row + CBZ), reusing RemoveFractionalChapters' removable rule and its
// files-before-commit transaction ordering — but with NO per-chapter selection: the
// whole removable set is swept at once. This cleans the fractionals that were
// already downloaded BEFORE the owner ticked ignore_fractional (the toggle itself
// deletes nothing). Returns the count removed.
//
// 🔴 OWNER-TRIGGERED ONLY (via DedupeFiles). The resurrection guard is intact — a
// fractional a non-ignored source also carries is NOT removable — and the feed rows
// are kept, so un-ticking the toggle re-ingests + re-downloads it.
func (s *Service) removeIgnoredDownloadedFractionals(ctx context.Context, id uuid.UUID) (int, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("series.DedupeFiles: begin ignored-fractional tx: %w", err)
	}

	removed, err := s.removeIgnoredDownloadedFractionalsInTx(ctx, tx, id)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("series.DedupeFiles: commit ignored-fractional tx: %w", err)
	}
	return removed, nil
}

// removeIgnoredDownloadedFractionalsInTx is the body of
// removeIgnoredDownloadedFractionals, run inside one transaction. The series
// (chapters + providers + feeds) is loaded through the TX client so the removable
// decision and the deletes share ONE snapshot. An unknown id yields
// ErrSeriesNotFound; every other error rolls the caller's transaction back.
func (s *Service) removeIgnoredDownloadedFractionalsInTx(ctx context.Context, tx *ent.Tx, id uuid.UUID) (int, error) {
	row, err := loadSeriesForCleanup(ctx, tx.Client(), id)
	if err != nil {
		return 0, err
	}
	// removableFractionals already requires downloaded + filed + fractional + >=1
	// carrier + every carrier ignored — the exact DOWNLOADED removable rule.
	return s.deleteRemovableTargets(ctx, tx, id, row, removableFractionals(row))
}

// mergeDuplicatesInTx is the body of mergeEngineSwitchDuplicates, run inside one
// transaction. The series (chapters + providers + feeds) is loaded through the TX
// client so the detection and the deletes share ONE snapshot. Every error rolls the
// caller's transaction back.
func (s *Service) mergeDuplicatesInTx(ctx context.Context, tx *ent.Tx, id uuid.UUID) (int, error) {
	row, err := loadSeriesForMerge(ctx, tx.Client(), id)
	if err != nil {
		return 0, err
	}

	pairs := detectEngineSwitchDuplicates(row)
	if len(pairs) == 0 {
		return 0, nil
	}

	for _, p := range pairs {
		if err := transferReadState(ctx, tx, p); err != nil {
			return 0, err
		}
		if err := deleteMergedChapter(ctx, tx, row, p.remove); err != nil {
			return 0, err
		}
	}

	// Files LAST, before the caller commits: a disk failure rolls back every row
	// change above, so a committed merge never leaves a duplicate CBZ orphaned.
	if err := s.removeMergedFiles(row, pairs); err != nil {
		return 0, err
	}
	return len(pairs), nil
}

// loadSeriesForMerge loads one series with everything the engine-switch merge needs
// in a single bounded query set: its chapters, its providers WITH their availability
// feeds (the URL identity carriers), and its category (the disk folder). The client
// is a parameter (not s.client) so the merge can pass the TRANSACTION's client and
// share one snapshot with the deletes. An unknown id yields ErrSeriesNotFound.
func loadSeriesForMerge(ctx context.Context, client *ent.Client, id uuid.UUID) (*ent.Series, error) {
	row, err := client.Series.Query().
		Where(entseries.IDEQ(id)).
		WithCategory().
		WithChapters().
		WithProviders(func(pq *ent.SeriesProviderQuery) {
			pq.WithProviderChapters()
		}).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrSeriesNotFound
		}
		return nil, fmt.Errorf("series.DedupeFiles: load series %s for merge: %w", id, err)
	}
	return row, nil
}

// detectEngineSwitchDuplicates finds the provable engine-switch duplicate pairs in
// one loaded series, purely in memory over the eager-loaded edges (no query).
//
// 🔴 THE IDENTITY JOIN — matched by the SOURCE CHAPTER URL, never by number or name.
// A physical chapter's source-relative URL (ProviderChapter.url) is stable across
// both the Suwayomi and Rensaio ingests, so it is the ONE authoritative identity
// that ties the negative-numeric row to its name-keyed twin. Matching by number is
// impossible (the whole bug is that the two rows carry DIFFERENT numbers/keys) and
// matching by fuzzy name would risk deleting a genuinely distinct chapter.
//
// 🔴 CONSERVATIVE — only PROVABLE pairs are offered. A pair is offered only when a
// non-empty URL is carried by EXACTLY two of the series' chapters, one of them
// negative-numeric-keyed and the other name-keyed, and nothing else. Any ambiguity —
// a URL shared by a normal-numbered chapter, by two negatives, by two name-keys, or
// by more than two chapters — is skipped entirely (it cannot be proven a clean
// engine-switch pair). A chapter already claimed by an earlier pair is never reused.
// URLs are walked in sorted order so the pairing is deterministic.
func detectEngineSwitchDuplicates(row *ent.Series) []duplicatePair {
	urlToChapters := indexChaptersBySourceURL(row)

	urls := make([]string, 0, len(urlToChapters))
	for u := range urlToChapters {
		urls = append(urls, u)
	}
	sort.Strings(urls)

	var pairs []duplicatePair
	claimed := make(map[uuid.UUID]struct{})
	for _, u := range urls {
		neg, named, ok := negNamedPair(urlToChapters[u])
		if !ok {
			continue
		}
		if _, dup := claimed[neg.ID]; dup {
			continue
		}
		if _, dup := claimed[named.ID]; dup {
			continue
		}
		pairs = append(pairs, duplicatePair{keep: named, remove: neg})
		claimed[neg.ID] = struct{}{}
		claimed[named.ID] = struct{}{}
	}
	return pairs
}

// indexChaptersBySourceURL groups the series' chapters by the source chapter URL
// their feed rows carry: url -> the distinct chapters (by id) some ProviderChapter
// row addresses at that url. Empty urls and feed rows whose key no chapter carries
// are skipped. This is the raw material for the URL identity join.
func indexChaptersBySourceURL(row *ent.Series) map[string]map[uuid.UUID]*ent.Chapter {
	chapterByKey := make(map[string]*ent.Chapter, len(row.Edges.Chapters))
	for _, ch := range row.Edges.Chapters {
		chapterByKey[ch.ChapterKey] = ch
	}

	urlToChapters := make(map[string]map[uuid.UUID]*ent.Chapter)
	for _, sp := range row.Edges.Providers {
		for _, pc := range sp.Edges.ProviderChapters {
			if pc.URL == "" {
				continue
			}
			ch, ok := chapterByKey[pc.ChapterKey]
			if !ok {
				continue
			}
			bucket := urlToChapters[pc.URL]
			if bucket == nil {
				bucket = make(map[uuid.UUID]*ent.Chapter)
				urlToChapters[pc.URL] = bucket
			}
			bucket[ch.ID] = ch
		}
	}
	return urlToChapters
}

// negNamedPair inspects the chapters that share a single URL and, ONLY when they are
// exactly one negative-numeric-keyed chapter and one name-keyed chapter, returns
// (negative, named, true). Any other shape — a different count, a normal-numbered
// key, two of a kind — returns ok=false so the URL is skipped (conservative).
func negNamedPair(chapters map[uuid.UUID]*ent.Chapter) (neg, named *ent.Chapter, ok bool) {
	if len(chapters) != 2 {
		return nil, nil, false
	}
	for _, ch := range chapters {
		switch {
		case isNegativeNumericKey(ch.ChapterKey):
			if neg != nil {
				return nil, nil, false
			}
			neg = ch
		case isNameKey(ch.ChapterKey):
			if named != nil {
				return nil, nil, false
			}
			named = ch
		default:
			return nil, nil, false
		}
	}
	if neg == nil || named == nil {
		return nil, nil, false
	}
	return neg, named, true
}

// isNegativeNumericKey reports whether a chapter_key is a negative numeric literal
// ("-1", "-2", "-1.5") — the Suwayomi engine's artifact for an epilogue it numbered
// below zero. A name-keyed ("name:…") or normal ("12") key never parses as < 0.
func isNegativeNumericKey(key string) bool {
	v, err := strconv.ParseFloat(key, 64)
	return err == nil && v < 0
}

// isNameKey reports whether a chapter_key is name-derived ("name:<slug>") — the
// go-forward Rensaio key for an unparseable chapter title, and the canonical row a
// merge keeps.
func isNameKey(key string) bool {
	return strings.HasPrefix(key, "name:")
}

// transferReadState carries the removed twin's read progress onto the kept canonical
// chapter — READ WINS, but only when the removed one was read AND the kept one was
// not, so a merge can never DOWNGRADE an already-read canonical. A no-op otherwise.
func transferReadState(ctx context.Context, tx *ent.Tx, p duplicatePair) error {
	if !p.remove.Read || p.keep.Read {
		return nil
	}
	upd := tx.Chapter.UpdateOneID(p.keep.ID).
		SetRead(true).
		SetLastReadPage(p.remove.LastReadPage)
	if p.remove.ReadAt != nil {
		upd.SetReadAt(*p.remove.ReadAt)
	}
	if _, err := upd.Save(ctx); err != nil {
		return fmt.Errorf("series.DedupeFiles: transfer read-state to chapter %s: %w", p.keep.ID, err)
	}
	return nil
}

// deleteMergedChapter removes the negative-numeric legacy chapter row and its
// now-orphaned ProviderChapter feed rows. The feed rows keyed by the removed key are
// orphaned by construction: chapter_key is UNIQUE per series, so the row just deleted
// was the only chapter carrying that key — no live chapter can reference those feed
// rows again, and leaving them would risk resurrecting the duplicate on a future
// ingest/reconcile. The kept canonical keeps its OWN (name-keyed) feed rows, which
// still carry the same source URL, so the source's offering of the chapter survives.
func deleteMergedChapter(ctx context.Context, tx *ent.Tx, row *ent.Series, remove *ent.Chapter) error {
	if err := tx.Chapter.DeleteOneID(remove.ID).Exec(ctx); err != nil {
		return fmt.Errorf("series.DedupeFiles: delete duplicate chapter %s: %w", remove.ID, err)
	}

	spIDs := make([]uuid.UUID, 0, len(row.Edges.Providers))
	for _, sp := range row.Edges.Providers {
		spIDs = append(spIDs, sp.ID)
	}
	if len(spIDs) == 0 {
		return nil
	}
	if _, err := tx.ProviderChapter.Delete().Where(
		entproviderchapter.SeriesProviderIDIn(spIDs...),
		entproviderchapter.ChapterKey(remove.ChapterKey),
	).Exec(ctx); err != nil {
		return fmt.Errorf("series.DedupeFiles: delete orphaned provider-chapters for key %q of series %s: %w",
			remove.ChapterKey, row.ID, err)
	}
	return nil
}

// removeMergedFiles deletes each merged duplicate's CBZ from the series folder. It
// runs BEFORE the commit, so a failure rolls the row deletions back and no committed
// merge can leave an orphan CBZ for disk.Reconcile to re-import.
//
// 🔴 A file that is NOT THERE is logged, not failed (mirrors RemoveFractionalChapters
// and DeleteSeries): the removed chapter may never have been downloaded, or the
// on-disk title/category may have drifted from the DB. Hard-failing would make a
// retry — whose earlier files ARE already gone — permanently unable to complete, so
// the honest signal is a WARN naming the series, the chapter key and the filename.
func (s *Service) removeMergedFiles(row *ent.Series, pairs []duplicatePair) error {
	categoryName := category.NameOf(row)
	for _, p := range pairs {
		removed, err := disk.RemoveChapterFile(s.storage, categoryName, row.Title, p.remove.Filename)
		if err != nil {
			return fmt.Errorf("%w: series %s chapter %s (%q): %w",
				ErrDedupeMergeFailed, row.ID, p.remove.ID, p.remove.Filename, err)
		}
		if !removed {
			slog.Warn("series.DedupeFiles: no CBZ found for the merged duplicate chapter — nothing deleted on disk (never downloaded, or the on-disk title/category drifted from the DB)",
				"series_id", row.ID, "title", row.Title, "category", categoryName,
				"chapter_id", p.remove.ID, "chapter_key", p.remove.ChapterKey, "filename", p.remove.Filename)
		}
	}
	return nil
}
