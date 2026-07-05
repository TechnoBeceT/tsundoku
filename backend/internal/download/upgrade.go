package download

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// upgradeResult holds the artefacts produced by fetchAndRender so that
// Upgrade can persist them in a single update call.
type upgradeResult struct {
	pc          *ent.ProviderChapter
	sp          *ent.SeriesProvider
	importance  int
	newFilename string
	pageCount   int
}

// DetectUpgrades scans all Chapter rows in state=downloaded and transitions
// those that have a strictly better source available to state=upgrade_available.
//
// "Strictly better" means: the maximum importance among the LIVE sources offering
// this chapter's key within the same series is STRICTLY GREATER THAN the chapter's
// satisfied_importance. An equal-importance source does NOT trigger an upgrade
// (comparison is >, not >=). A LIVE source is one that still has retry budget
// (attempts < maxRetries) AND is past its per-source cooldown — the same predicate
// the download path uses (chapter.RankedLiveCandidates). So a source that failed
// out of the download path (exhausted) is never chosen as an upgrade target, and a
// source merely on cooldown after an upgrade attempt is skipped THIS cycle but
// re-considered once its cooldown elapses (upgrade failures never spend budget, so
// a preferred source always recovers as an upgrade target).
//
// Chapters with a nil satisfied_importance are skipped with a warning — this is
// a defensive case, because a successful download always sets satisfied_importance.
//
// Returns the number of chapters flagged. now is read once by the caller so every
// chapter in the scan sees a consistent cooldown horizon.
func DetectUpgrades(ctx context.Context, client *ent.Client, maxRetries int) (int, error) {
	now := time.Now()
	chapters, err := client.Chapter.Query().
		Where(entchapter.StateEQ(entchapter.StateDownloaded)).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("download.DetectUpgrades: query downloaded chapters: %w", err)
	}

	flagged := 0
	for _, ch := range chapters {
		n, err := detectUpgradeForChapter(ctx, client, ch, maxRetries, now)
		if err != nil {
			return flagged, err
		}
		flagged += n
	}
	return flagged, nil
}

// detectUpgradeForChapter evaluates a single chapter and transitions it to
// upgrade_available when a strictly higher-importance provider exists.
// Returns 1 if flagged, 0 if skipped or unchanged, and a non-nil error only
// for hard failures (state transition errors) that should abort the scan.
func detectUpgradeForChapter(ctx context.Context, client *ent.Client, ch *ent.Chapter, maxRetries int, now time.Time) (int, error) {
	// Defensive path: satisfied_importance should always be set for a downloaded
	// chapter (a successful download always writes it). Skip to avoid a nil-deref.
	if ch.SatisfiedImportance == nil {
		slog.WarnContext(ctx, "download.DetectUpgrades: downloaded chapter has nil satisfied_importance — skipping",
			"chapter_id", ch.ID,
			"chapter_key", ch.ChapterKey,
		)
		return 0, nil
	}

	maxImportance, err := maxImportanceForChapter(ctx, client, ch, maxRetries, now)
	if err != nil {
		// Log and continue — one chapter failing to scan should not abort all others.
		slog.WarnContext(ctx, "download.DetectUpgrades: failed to query max importance for chapter — skipping",
			"chapter_id", ch.ID,
			"err", err,
		)
		return 0, nil
	}

	// Strict comparison: only flag when a strictly higher-importance source exists.
	if maxImportance <= *ch.SatisfiedImportance {
		return 0, nil
	}

	if err := chapter.SetState(ctx, client, ch.ID, entchapter.StateUpgradeAvailable); err != nil {
		return 0, fmt.Errorf("download.DetectUpgrades: transition chapter %s to upgrade_available: %w", ch.ID, err)
	}
	return 1, nil
}

// maxImportanceForChapter returns the highest importance among the LIVE sources
// offering ch's chapter_key within the same series (attempts < maxRetries AND past
// cooldown). Returns 0 if no eligible source exists. It reuses
// chapter.RankedLiveCandidates so the "live, importance-ranked" rule is defined
// once and is identical to the download path (§2 DRY).
func maxImportanceForChapter(ctx context.Context, client *ent.Client, ch *ent.Chapter, maxRetries int, now time.Time) (int, error) {
	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, maxRetries, now)
	if err != nil {
		return 0, fmt.Errorf("rank live candidates for chapter %s: %w", ch.ID, err)
	}
	if len(cands) == 0 {
		return 0, nil
	}
	// RankedLiveCandidates is importance-DESC, so the first is the highest.
	return cands[0].SeriesProvider.Importance, nil
}

// Upgrade executes a non-destructive atomic upgrade for the given chapter.
//
// Flow (success path):
//  1. Load the chapter; transition upgrade_available → upgrading; broadcast upgrade.start.
//  2. Fetch pages from the best provider and render the new CBZ atomically.
//  3. Persist updated provenance; clear last_error; transition upgrading → downloaded;
//     broadcast download.done.
//  4. Best-effort delete the old CBZ if the filename changed (different provider/scanlator
//     ⇒ different name); log on failure but do not fail the upgrade.
//
// Failure path (fetch or render error):
//   - Does NOT modify the existing file or its provenance.
//   - Transitions upgrading → downloaded (working copy retained).
//   - Records last_error; broadcasts upgrade.fail.
//   - Returns nil — an upgrade failure is a handled outcome, not a hard error.
func (d *Dispatcher) Upgrade(ctx context.Context, chapterID uuid.UUID) error {
	ch, err := d.client.Chapter.Query().
		Where(entchapter.IDEQ(chapterID)).
		WithSeries(func(sq *ent.SeriesQuery) { sq.WithCategory() }).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.Upgrade: load chapter %s: %w", chapterID, err)
	}

	if err := chapter.SetState(ctx, d.client, chapterID, entchapter.StateUpgrading); err != nil {
		return fmt.Errorf("download.Dispatcher.Upgrade: transition to upgrading for chapter %s: %w", chapterID, err)
	}
	d.broadcast("upgrade.start", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateUpgrading),
	})

	// The upgrade fetch honours the SAME per-source concurrency cap as the download
	// path (read at use-time). Upgrades are driven sequentially by the job runner
	// today, so this limiter rarely blocks; it is here so an upgrade fetch is capped
	// per provider exactly like a download fetch, and stays correct if upgrades are
	// ever parallelised.
	limiter := newProviderLimiter(d.downloadConcurrency(ctx))
	res, err := d.fetchAndRender(ctx, ch, chapterID, limiter)
	if err != nil {
		return d.handleUpgradeFailure(ctx, chapterID, res.pc, err)
	}

	if err := d.persistUpgradeSuccess(ctx, chapterID, res); err != nil {
		// A persist failure is a DB error, not the source's fault — no per-source
		// bump (failedPC is nil).
		return d.handleUpgradeFailure(ctx, chapterID, nil, err)
	}

	d.broadcast("download.done", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateDownloaded),
	})

	d.tryDeleteOldCBZ(ctx, chapterID, ch, res.newFilename)
	return nil
}

// fetchAndRender resolves the best LIVE source for chapterID, fetches pages, and
// renders the new CBZ atomically. It returns an upgradeResult on success, or an
// error to route to handleUpgradeFailure. On a FETCH failure the returned result
// carries the attempted source's pc so the caller can COOL IT DOWN (defer the next
// try) — upgrade failures never spend retry budget, so a preferred source recovers
// as an upgrade target once it is back. A render failure returns no pc (not the
// source's fault, so no cooldown).
func (d *Dispatcher) fetchAndRender(ctx context.Context, ch *ent.Chapter, chapterID uuid.UUID, limiter *providerLimiter) (upgradeResult, error) {
	cands, err := chapter.RankedLiveCandidates(ctx, d.client, chapterID, d.retry.MaxRetries(ctx), time.Now())
	if err != nil {
		return upgradeResult{}, fmt.Errorf("rank live candidates: %w", err)
	}
	if len(cands) == 0 {
		// Defensive path: DetectUpgrades only flags a chapter when a live higher
		// source exists, so there is normally at least one candidate here; a
		// concurrent RemoveProvider / owner action, or a cooldown elapsing between
		// the flag and this call, could empty it.
		return upgradeResult{}, fmt.Errorf("no live source available for chapter %s", chapterID)
	}
	best := cands[0]
	pc := best.ProviderChapter
	sp := best.SeriesProvider

	release := limiter.acquire(sp.Provider)
	pages, err := d.f.Fetch(ctx, buildFetchRef(pc, sp))
	release()
	if err != nil {
		// Carry pc so handleUpgradeFailure bumps this source's per-source retry state.
		return upgradeResult{pc: pc, sp: sp}, err
	}

	maxChap := maxChapterNumber(ctx, d.client, ch.SeriesID)
	newFilename, err := disk.RenderChapter(disk.RenderRequest{
		Storage: d.cfg.Storage,
		Meta:    buildRenderMeta(ch, pc, sp, maxChap),
		Pages:   pages.Pages,
	})
	if err != nil {
		return upgradeResult{}, err
	}

	return upgradeResult{
		pc:          pc,
		sp:          sp,
		importance:  sp.Importance,
		newFilename: newFilename,
		pageCount:   pages.PageCount,
	}, nil
}

// persistUpgradeSuccess writes the new provenance to the Chapter row, resets the
// winning source's per-source retry state, and transitions the state from
// upgrading to downloaded. Returns an error only for DB failures that should be
// routed to handleUpgradeFailure.
func (d *Dispatcher) persistUpgradeSuccess(ctx context.Context, chapterID uuid.UUID, res upgradeResult) error {
	if err := d.client.Chapter.UpdateOneID(chapterID).
		SetSatisfiedByProviderID(res.sp.ID).
		SetSatisfiedImportance(res.importance).
		SetFilename(res.newFilename).
		SetPageCount(res.pageCount).
		SetDownloadDate(time.Now()).
		SetLastError("").
		Exec(ctx); err != nil {
		// The new CBZ has already been rendered — route through handleUpgradeFailure
		// so the chapter transitions out of upgrading. A partial state (new file on
		// disk, old DB provenance) may exist; Task 7 reconcile handles orphans.
		return fmt.Errorf("persist provenance: %w", err)
	}

	// The winning source works: clear any per-source retry state it accrued from
	// earlier failed upgrade attempts (parity with finishDownload's winning-source
	// reset), so a prior transient cooldown never lingers on a now-proven source.
	if err := d.client.ProviderChapter.UpdateOneID(res.pc.ID).
		SetAttempts(0).
		SetLastError("").
		ClearNextAttemptAt().
		Exec(ctx); err != nil {
		return fmt.Errorf("reset winning source retry state: %w", err)
	}

	// Defensive path: reachable only on DB failure between the provenance update
	// above and this state transition. If it fails, Upgrade routes through
	// handleUpgradeFailure, which sets state=downloaded (good state: new file on
	// disk + correct provenance already written), records last_error, and emits an
	// upgrade.fail event. That event is a misleading false-failure signal — no data
	// was lost and the upgrade actually succeeded — but it is harmless: Task 7
	// reconcile / the next DetectUpgrades run will observe state=downloaded with
	// satisfied_importance already at the new value and will not re-flag the chapter.
	if err := chapter.SetState(ctx, d.client, chapterID, entchapter.StateDownloaded); err != nil {
		return fmt.Errorf("transition to downloaded: %w", err)
	}

	return nil
}

// tryDeleteOldCBZ performs a best-effort removal of the old CBZ file when the
// filename changed (indicating a different provider/scanlator). It logs but
// does not fail on removal errors — Task 7 reconcile will clean up any orphans.
//
// It resolves the series' REAL category folder via the shared seriesCategoryName
// (the same resolver buildRenderMeta uses to WRITE the file), so the delete looks
// in the exact folder the CBZ was rendered into. Previously it hardcoded "Other",
// so upgrading a chapter in a non-Other series looked in the wrong folder and
// left the old CBZ orphaned. ch is loaded WithSeries(WithCategory()) by Upgrade.
func (d *Dispatcher) tryDeleteOldCBZ(ctx context.Context, chapterID uuid.UUID, ch *ent.Chapter, newFilename string) {
	oldFilename := ch.Filename
	if oldFilename == "" || oldFilename == newFilename {
		return
	}
	seriesTitle := ""
	if ch.Edges.Series != nil {
		seriesTitle = ch.Edges.Series.Title
	}
	oldPath := filepath.Join(disk.SeriesDir(d.cfg.Storage, seriesCategoryName(ch), seriesTitle), oldFilename)
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		slog.WarnContext(ctx, "download.Dispatcher.Upgrade: best-effort delete of old CBZ failed — Task 7 reconcile will clean it up",
			"chapter_id", chapterID,
			"old_path", oldPath,
			"err", err,
		)
	}
}

// handleUpgradeFailure is the upgrade-specific failure handler.
//
// Unlike a download failure, an upgrade failure MUST keep the working copy
// intact: it transitions upgrading → downloaded (the chapter stays usable with
// its original CBZ and provenance), records last_error, and broadcasts
// upgrade.fail. When the failure came from a fetch attempt (failedPC non-nil) it
// COOLS THE SOURCE DOWN (cooldownSource — defers the next upgrade try) WITHOUT
// spending its retry budget, so a source temporarily down during upgrade attempts
// never exhausts and always recovers as an upgrade target once it is back (the
// "preferred source recovers → swap back" guarantee). It always returns nil so
// callers treat upgrade failures as handled outcomes, not infrastructure errors.
func (d *Dispatcher) handleUpgradeFailure(ctx context.Context, chapterID uuid.UUID, failedPC *ent.ProviderChapter, cause error) error {
	if failedPC != nil {
		d.cooldownSource(ctx, failedPC, cause, time.Now())
	}

	// Transition upgrading → downloaded (restores working state).
	if setErr := chapter.SetState(ctx, d.client, chapterID, entchapter.StateDownloaded); setErr != nil {
		// Defensive path: only reachable if the DB connection is lost between the
		// upgrading transition and this failure handler. Log but still return nil
		// so the chapter does not permanently strand in upgrading on a transient
		// DB error — the next DetectUpgrades run will re-flag it if needed.
		slog.ErrorContext(ctx, "download.Dispatcher.handleUpgradeFailure: could not transition upgrading→downloaded — chapter may be stranded",
			"chapter_id", chapterID,
			"cause", cause,
			"set_state_err", setErr,
		)
		return nil
	}

	if err := d.client.Chapter.UpdateOneID(chapterID).
		SetLastError(cause.Error()).
		Exec(ctx); err != nil {
		slog.WarnContext(ctx, "download.Dispatcher.handleUpgradeFailure: could not persist last_error",
			"chapter_id", chapterID,
			"err", err,
		)
	}

	d.broadcast("upgrade.fail", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateDownloaded),
		Error:     cause.Error(),
	})
	return nil
}
