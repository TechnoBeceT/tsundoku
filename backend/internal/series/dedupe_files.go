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

// DedupeReason names WHICH pass a planned removal came from, so the preview UI can
// group the items the owner is about to delete. The three reasons mirror the three
// deletion sources described on DedupeFiles.
type DedupeReason string

const (
	// DedupeReasonEpilogueMerge is a pass-0 engine-switch duplicate: a negative-numeric
	// legacy chapter row (+ its orphaned feed + its CBZ) merged into its name-keyed twin.
	DedupeReasonEpilogueMerge DedupeReason = "epilogue-merge"
	// DedupeReasonIgnoredFractional is a pass-0b downloaded fractional whose every
	// carrier now ignores fractionals: its Chapter row + CBZ are removed.
	DedupeReasonIgnoredFractional DedupeReason = "ignored-fractional"
	// DedupeReasonOrphanSuperseded is a passes-1/2 file-only removal: a duplicate CBZ
	// of a downloaded chapter's winning file, or a superseded fractional part's
	// orphaned CBZ. NO DB row is touched.
	DedupeReasonOrphanSuperseded DedupeReason = "orphan-superseded"
)

// DedupePlanItemDTO is one removal the DedupeFiles sweep WOULD perform: the CBZ
// filename that goes, the chapter number it belongs to (nullable — a name-keyed
// merge twin or an un-numbered file has none), and the reason/pass it came from so
// the confirm dialog can group it.
type DedupePlanItemDTO struct {
	Reason   string   `json:"reason"`
	Number   *float64 `json:"number"`
	Filename string   `json:"filename"`
}

// DedupePlanDTO is the DedupeFiles dry-run: the exact set of removals the owner is
// about to confirm. Total is len(Items); Items is always non-nil so the JSON renders
// [] rather than null. Because both the preview and the executor derive from the SAME
// plan (dedupeFilesPlan), this list is provably identical to what a subsequent
// POST /api/series/:id/dedupe-files deletes.
type DedupePlanDTO struct {
	Total int                 `json:"total"`
	Items []DedupePlanItemDTO `json:"items"`
}

// dedupePlan is the resolved set of everything DedupeFiles would remove for one
// series, computed ONCE (read-only) and consumed by BOTH DedupeFilesPreview (the
// dry-run) and DedupeFiles (the executor) — so the preview list can never drift from
// what execute deletes (the whole point of the plan). Each field carries the
// machinery its pass needs to actually delete:
//   - mergePairs: pass 0 — Chapter ROW + orphaned feed rows + CBZ, in a transaction.
//   - ignoredFractionals: pass 0b — Chapter ROW + CBZ, in a transaction.
//   - fileOnly: passes 1+2 — orphan/duplicate CBZ files only, best-effort, no DB.
type dedupePlan struct {
	mergePairs         []duplicatePair
	ignoredFractionals []*ent.Chapter
	fileOnly           []dedupeFileItem
}

// dedupeFileItem is one file-only (passes 1+2) removal: the CBZ filename and the
// chapter number the sweep matched it against (for the preview label).
type dedupeFileItem struct {
	number   *float64
	filename string
}

// DedupeFilesPreview returns the DedupeFiles DRY-RUN for one series: the exact list
// of CBZ files (and the chapter rows behind the pass-0/0b items) that a subsequent
// POST /api/series/:id/dedupe-files would delete, grouped by reason. It DELETES
// NOTHING and writes NOTHING — a read-only projection of dedupeFilesPlan, the same
// plan the executor consumes. A missing series yields ErrSeriesNotFound (404).
func (s *Service) DedupeFilesPreview(ctx context.Context, id uuid.UUID) (DedupePlanDTO, error) {
	plan, _, err := s.dedupeFilesPlan(ctx, s.client, id)
	if err != nil {
		return DedupePlanDTO{}, err
	}
	return plan.toDTO(), nil
}

// DedupeFiles is the owner-triggered duplicate cleanup for a whole series. It removes
// everything dedupeFilesPlan resolves and returns the total number of
// duplicates/leftovers it deleted, across THREE sources:
//
//  0. Engine-switch duplicate CHAPTER-ROW merge (see detectEngineSwitchDuplicates):
//     the Suwayomi→Rensaio switch keyed the SAME physical chapter twice — once as a
//     negative-numeric literal ("-1", from Suwayomi's "number = -1" epilogue) and
//     once name-keyed ("name:epilogue", from Rensaio reporting it unparseable) — so
//     UNIQUE(series_id, chapter_key) never deduped them and BOTH the Chapter row and
//     its CBZ persist twice. This pass merges each PROVABLE pair (matched by the
//     source chapter URL on ProviderChapter, the true identity across both ingests),
//     keeping the name-keyed canonical and deleting the negative-numeric legacy row +
//     its orphaned feed rows + its CBZ. Runs in its own transaction with
//     files-deleted-before-commit + rollback.
//     0b. Ignored DOWNLOADED fractional cleanup (see removableFractionals): every
//     fractional chapter that is DOWNLOADED and whose EVERY carrier now ignores
//     fractionals — i.e. was downloaded BEFORE the owner ticked ignore_fractional on
//     its source(s) — is deleted (row + CBZ), the whole-series counterpart of the
//     per-chapter RemoveFractionalChapters. It reuses that endpoint's exact removable
//     rule (fractional AND >=1 carrier AND every carrier ignored — the resurrection
//     guard) and its files-before-commit ordering. Like pass 0 it deletes Chapter
//     ROWs, in its own transaction, never automatically.
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
	plan, row, err := s.dedupeFilesPlan(ctx, s.client, id)
	if err != nil {
		return 0, err
	}

	// DB-mutating passes first (each in its own tx, delete rows), then the file-only
	// sweep. The plan already resolved the file-only set as-if the pass-0/0b rows were
	// gone (their files are excluded), so it is safe to delete them last.
	merged, err := s.executeMergePlan(ctx, row, plan.mergePairs)
	if err != nil {
		return merged, err
	}

	ignoredRemoved, err := s.executeIgnoredFractionalPlan(ctx, id, row, plan.ignoredFractionals)
	total := merged + ignoredRemoved
	if err != nil {
		return total, err
	}

	total += s.executeFileOnlyPlan(row, plan.fileOnly)
	return total, nil
}

// dedupeFilesPlan resolves the COMPLETE removal set for one series, read-only, in one
// bounded query set plus one directory read per swept chapter number. It is the
// single source of truth behind both DedupeFilesPreview and DedupeFiles. The client
// is a parameter so it can run against s.client (preview) or a transaction's client;
// it returns the loaded series too (the executor reuses it for the delete machinery).
// An unknown id yields ErrSeriesNotFound.
//
// The file-only set (passes 1+2) is resolved AS-IF the pass-0/0b rows were already
// deleted: those chapters are excluded and their filenames are seeded into the
// planned set, so the plan matches the executor's sequenced behaviour (pass 0/0b
// commit before the file sweep runs) without mutating anything.
func (s *Service) dedupeFilesPlan(ctx context.Context, client *ent.Client, id uuid.UUID) (dedupePlan, *ent.Series, error) {
	row, err := loadSeriesForCleanup(ctx, client, id)
	if err != nil {
		return dedupePlan{}, nil, err
	}

	mergePairs := detectEngineSwitchDuplicates(row)

	// Pass 0 (merge) and pass 0b (ignored fractionals) are resolved from the SAME
	// pre-deletion snapshot, so they are NOT inherently disjoint: a negative-FRACTIONAL
	// merge twin (chapter_key "-1.5" — isNegativeNumericKey contemplates it, and
	// chapterrange.IsFractional(-1.5) is true) that is ALSO downloaded, filed, and
	// carried by >=1 ignore-fractional feed row satisfies BOTH rules. Leaving it in both
	// would (a) emit its file twice and inflate the preview Total, breaking the
	// dry-run==execute parity this plan exists to guarantee, and (b) on execute ERROR:
	// executeMergePlan commits the twin's row deletion, then executeIgnoredFractionalPlan
	// re-deletes the same id and trips deleted != len(targets) → ErrChapterNotRemovable
	// (a spurious 500). MERGE WINS — the old sequenced executor was immune because pass 0
	// committed and pass 0b then RELOADED through the tx client, so the deleted twin was
	// invisible to removableFractionals; excluding every merge-removed id restores that.
	mergeRemoved := make(map[uuid.UUID]struct{}, len(mergePairs))
	for _, p := range mergePairs {
		mergeRemoved[p.remove.ID] = struct{}{}
	}
	ignoredFractionals := excludeMergeRemoved(removableFractionals(row), mergeRemoved)

	// The rows the pass-0/0b deletes claim: exclude them from the file-only sweep so a
	// merged twin or an ignored fractional is never also counted as a file-only orphan.
	removedChapters := make(map[uuid.UUID]struct{}, len(mergePairs)+len(ignoredFractionals))
	claimedFiles := make(map[string]struct{}, len(mergePairs)+len(ignoredFractionals))
	for _, p := range mergePairs {
		removedChapters[p.remove.ID] = struct{}{}
		if p.remove.Filename != "" {
			claimedFiles[p.remove.Filename] = struct{}{}
		}
	}
	for _, ch := range ignoredFractionals {
		removedChapters[ch.ID] = struct{}{}
		if ch.Filename != "" {
			claimedFiles[ch.Filename] = struct{}{}
		}
	}

	fileOnly, err := s.planFileOnlySweeps(row, removedChapters, claimedFiles)
	if err != nil {
		return dedupePlan{}, nil, err
	}

	return dedupePlan{
		mergePairs:         mergePairs,
		ignoredFractionals: ignoredFractionals,
		fileOnly:           fileOnly,
	}, row, nil
}

// excludeMergeRemoved drops every chapter the merge pass (pass 0) will delete from the
// ignored-downloaded-fractional set (pass 0b), keeping the two passes disjoint. A
// negative-FRACTIONAL merge twin can satisfy BOTH rules at once; without this filter it
// would be planned (and, on execute, deleted) twice — see dedupeFilesPlan for the full
// double-claim. Merge wins, so the fractional set yields to it.
func excludeMergeRemoved(fractionals []*ent.Chapter, mergeRemoved map[uuid.UUID]struct{}) []*ent.Chapter {
	if len(mergeRemoved) == 0 {
		return fractionals
	}
	kept := make([]*ent.Chapter, 0, len(fractionals))
	for _, ch := range fractionals {
		if _, removed := mergeRemoved[ch.ID]; removed {
			continue
		}
		kept = append(kept, ch)
	}
	return kept
}

// planFileOnlySweeps resolves passes 1 (winning-chapter duplicates) and 2 (superseded
// fractional-part orphans) as a list of CBZ filenames to remove, WITHOUT deleting
// anything — the read-only enumerator behind the executor's best-effort file sweep.
//
// removedChapters are the rows the pass-0/0b deletes claim (skipped here); claimedFiles
// seeds the running "already planned" set so a file the pass-0/0b already owns, or one
// pass 1 already listed, is never listed twice (which would inflate the count). Files
// are enumerated in the series' loaded (number-ASC) chapter order, replicating the
// executor's per-chapter re-read semantics exactly.
func (s *Service) planFileOnlySweeps(row *ent.Series, removedChapters map[uuid.UUID]struct{}, claimedFiles map[string]struct{}) ([]dedupeFileItem, error) {
	categoryName := category.NameOf(row)
	planned := make(map[string]struct{}, len(claimedFiles))
	for f := range claimedFiles {
		planned[f] = struct{}{}
	}

	items, err := s.planWinningDuplicates(categoryName, row, removedChapters, planned)
	if err != nil {
		return nil, err
	}
	orphans, err := s.planSupersededOrphans(categoryName, row, removedChapters, planned)
	if err != nil {
		return nil, err
	}
	return append(items, orphans...), nil
}

// planWinningDuplicates is DedupeFiles pass 1: for every downloaded chapter with a
// winning filename AND a number (not claimed by pass 0/0b), list the OTHER CBZs of
// that number — the winner is kept by ListOtherChapterFiles.
func (s *Service) planWinningDuplicates(categoryName string, row *ent.Series, removedChapters map[uuid.UUID]struct{}, planned map[string]struct{}) ([]dedupeFileItem, error) {
	var items []dedupeFileItem
	for _, ch := range row.Edges.Chapters {
		if _, gone := removedChapters[ch.ID]; gone {
			continue
		}
		if ch.Filename == "" || ch.Number == nil {
			continue
		}
		listed, err := s.listFileOnly(categoryName, row.Title, *ch.Number, ch.Filename, planned)
		if err != nil {
			return nil, fmt.Errorf("series.DedupeFiles: chapter %s: %w", ch.ID, err)
		}
		items = append(items, listed...)
	}
	return items, nil
}

// planSupersededOrphans is DedupeFiles pass 2: for every superseded FRACTIONAL-part
// chapter (not claimed by pass 0/0b), list EVERY CBZ of that number with no keeper.
// Whole-integer superseded chapters are skipped — their file, if any, is a legitimate
// keeper elsewhere.
func (s *Service) planSupersededOrphans(categoryName string, row *ent.Series, removedChapters map[uuid.UUID]struct{}, planned map[string]struct{}) ([]dedupeFileItem, error) {
	var items []dedupeFileItem
	for _, ch := range row.Edges.Chapters {
		if _, gone := removedChapters[ch.ID]; gone {
			continue
		}
		if ch.State != entchapter.StateSuperseded || ch.Number == nil || !chapterrange.IsFractional(*ch.Number) {
			continue
		}
		listed, err := s.listFileOnly(categoryName, row.Title, *ch.Number, "", planned)
		if err != nil {
			return nil, fmt.Errorf("series.DedupeFiles: superseded chapter %s: %w", ch.ID, err)
		}
		items = append(items, listed...)
	}
	return items, nil
}

// listFileOnly enumerates the removable duplicate CBZs for one chapter number
// (keeping keepFilename), skipping any filename already in planned and recording the
// rest into it — so the same file is never planned twice across passes/chapters.
func (s *Service) listFileOnly(categoryName, title string, number float64, keepFilename string, planned map[string]struct{}) ([]dedupeFileItem, error) {
	names, err := disk.ListOtherChapterFiles(s.storage, categoryName, title, chapter.FormatChapterNumber(number), keepFilename)
	if err != nil {
		return nil, err
	}
	out := make([]dedupeFileItem, 0, len(names))
	for _, name := range names {
		if _, dup := planned[name]; dup {
			continue
		}
		planned[name] = struct{}{}
		n := number
		out = append(out, dedupeFileItem{number: &n, filename: name})
	}
	return out, nil
}

// executeMergePlan runs pass 0 for the planned merge pairs in ONE transaction: each
// pair's read-state is transferred onto its canonical twin, the legacy row + orphaned
// feed rows are deleted, and the CBZs are removed BEFORE the commit so a disk failure
// rolls the row changes back (no committed merge can orphan a CBZ). EVERY pair's row is
// merged, but the returned count includes only the FILE-bearing pairs — the same set
// toDTO surfaces — so the executor's tally stays exactly equal to the preview Total (a
// row-only merge, twin never downloaded, is a silent dedup artifact with no CBZ to
// report).
func (s *Service) executeMergePlan(ctx context.Context, row *ent.Series, pairs []duplicatePair) (int, error) {
	if len(pairs) == 0 {
		return 0, nil
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("series.DedupeFiles: begin merge tx: %w", err)
	}

	merged := 0
	for _, p := range pairs {
		if err := s.applyMergePair(ctx, tx, row, p); err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		if p.remove.Filename != "" {
			merged++
		}
	}
	if err := s.removeMergedFiles(row, pairs); err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("series.DedupeFiles: commit merge tx: %w", err)
	}
	return merged, nil
}

// applyMergePair applies one merge pair's DB changes inside the caller's transaction:
// transfer read-state onto the canonical, then delete the legacy row + its feed rows.
func (s *Service) applyMergePair(ctx context.Context, tx *ent.Tx, row *ent.Series, p duplicatePair) error {
	if err := transferReadState(ctx, tx, p); err != nil {
		return err
	}
	return deleteMergedChapter(ctx, tx, row, p.remove)
}

// executeIgnoredFractionalPlan runs pass 0b for the planned ignored downloaded
// fractionals in ONE transaction, reusing deleteRemovableTargets (the shared
// files-before-commit delete guard). Returns the count removed. An empty plan is a
// no-op.
func (s *Service) executeIgnoredFractionalPlan(ctx context.Context, id uuid.UUID, row *ent.Series, targets []*ent.Chapter) (int, error) {
	if len(targets) == 0 {
		return 0, nil
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("series.DedupeFiles: begin ignored-fractional tx: %w", err)
	}

	removed, err := s.deleteRemovableTargets(ctx, tx, id, row, targets)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("series.DedupeFiles: commit ignored-fractional tx: %w", err)
	}
	return removed, nil
}

// executeFileOnlyPlan deletes the planned passes-1/2 orphan/duplicate CBZs. It is
// BEST-EFFORT per file (a genuine os.Remove failure is logged and skipped, mirroring
// RemoveOtherChapterFiles) and performs NO DB writes, so it never returns an error;
// it returns the count actually removed. A file already gone (removed=false) is not
// counted.
func (s *Service) executeFileOnlyPlan(row *ent.Series, items []dedupeFileItem) int {
	if len(items) == 0 {
		return 0
	}
	categoryName := category.NameOf(row)
	total := 0
	for _, it := range items {
		removed, err := disk.RemoveChapterFile(s.storage, categoryName, row.Title, it.filename)
		if err != nil {
			slog.Warn("series.DedupeFiles: best-effort delete of orphan/duplicate CBZ failed",
				"series_id", row.ID, "title", row.Title, "category", categoryName, "filename", it.filename, "err", err)
			continue
		}
		if removed {
			total++
		}
	}
	return total
}

// toDTO projects a plan onto the wire shape the preview returns: one item per FILE
// removal, grouped by reason, non-nil Items so the JSON is [] not null. A merge pair
// whose twin was never downloaded (Filename == "") removes only a duplicate ROW, no
// CBZ, so it is omitted here (an empty-filename row is a blank line in the confirm
// dialog) — executeMergePlan drops it from the returned count the same way, so the
// preview Total stays exactly equal to what execute reports.
func (p dedupePlan) toDTO() DedupePlanDTO {
	items := make([]DedupePlanItemDTO, 0, len(p.mergePairs)+len(p.ignoredFractionals)+len(p.fileOnly))
	for _, pair := range p.mergePairs {
		if pair.remove.Filename == "" {
			continue
		}
		items = append(items, DedupePlanItemDTO{
			Reason:   string(DedupeReasonEpilogueMerge),
			Number:   pair.remove.Number,
			Filename: pair.remove.Filename,
		})
	}
	for _, ch := range p.ignoredFractionals {
		items = append(items, DedupePlanItemDTO{
			Reason:   string(DedupeReasonIgnoredFractional),
			Number:   ch.Number,
			Filename: ch.Filename,
		})
	}
	for _, it := range p.fileOnly {
		items = append(items, DedupePlanItemDTO{
			Reason:   string(DedupeReasonOrphanSuperseded),
			Number:   it.number,
			Filename: it.filename,
		})
	}
	return DedupePlanDTO{Total: len(items), Items: items}
}

// duplicatePair is one engine-switch duplicate: keep is the name-keyed canonical
// chapter (the go-forward Rensaio key), remove is its negative-numeric legacy twin
// (the Suwayomi artifact). Both proven to be the SAME physical chapter by a shared
// source chapter URL — see detectEngineSwitchDuplicates.
type duplicatePair struct {
	keep   *ent.Chapter
	remove *ent.Chapter
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
