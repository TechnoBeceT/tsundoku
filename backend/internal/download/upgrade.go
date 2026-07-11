package download

import (
	"context"
	"errors"
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
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// errUpgradeNoLongerNeeded signals that fetchAndRender found the chapter's
// current satisfier is still the best live source, so no fetch is warranted (a
// stale upgrade_available flag). It is NOT a failure: Upgrade returns the chapter
// to downloaded cleanly (the watermark was already refreshed), without recording
// last_error or emitting upgrade.fail. This is the defence-in-depth partner to
// DetectUpgrades' self-churn guard.
var errUpgradeNoLongerNeeded = errors.New("upgrade no longer needed: current satisfier is already the best source")

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
// EFFECTIVE satisfied importance (see effectiveSatisfiedImportance — the CURRENT
// importance of the satisfying source while it is still attached, NOT the frozen
// satisfied_importance snapshot). An equal-importance source does NOT trigger an
// upgrade (comparison is >, not >=). A LIVE source is one that still has retry budget
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
//
// This is the GATE-FREE form (no source-politeness circuit-breaker). Production
// calls (*Dispatcher).DetectUpgrades, which additionally excludes a source whose
// breaker is tripped from being chosen as an upgrade target; this package
// function is kept for callers that hold no gate (tests, non-dispatcher callers)
// and is equivalent to the method with a nil gate.
func DetectUpgrades(ctx context.Context, client *ent.Client, maxRetries int) (int, error) {
	return detectUpgrades(ctx, client, nil, maxRetries)
}

// DetectUpgrades is the GATED form used in production. It excludes a source
// whose source-politeness circuit-breaker is tripped (cooled down) from being
// chosen as an upgrade target, so a Cloudflare-blocked higher-importance source
// is never FLAGGED for upgrade. This prevents an upgrade_available → upgrading →
// downloaded flag/revert flap every cycle while the source is down (the actual
// fetch would be blocked anyway by fetchAndRender's gate — this stops the churn
// at the source). A nil gate makes it identical to the package function.
func (d *Dispatcher) DetectUpgrades(ctx context.Context, maxRetries int) (int, error) {
	return detectUpgrades(ctx, d.client, d.gate, maxRetries)
}

// detectUpgrades is the shared implementation behind both DetectUpgrades forms.
// gate may be nil (gate-free): every gated-candidate exclusion is a no-op then.
func detectUpgrades(ctx context.Context, client *ent.Client, gate *sourcegate.Service, maxRetries int) (int, error) {
	now := time.Now()
	chapters, err := client.Chapter.Query().
		Where(entchapter.StateEQ(entchapter.StateDownloaded)).
		// Eager-load the satisfying source for the WHOLE scan in one batched query
		// (Ent resolves the satisfied_by edge with a single IN over the scanned
		// chapters' satisfied_by_provider_id). effectiveSatisfiedImportance then reads
		// each source's CURRENT importance straight off the loaded edge — never a
		// per-chapter lookup, so the watermark rule costs a constant query, not an N+1
		// on a library-wide scan.
		WithSatisfiedBy().
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("download.DetectUpgrades: query downloaded chapters: %w", err)
	}

	flagged := 0
	for _, ch := range chapters {
		n, err := detectUpgradeForChapter(ctx, client, gate, ch, maxRetries, now)
		if err != nil {
			return flagged, err
		}
		flagged += n
	}
	return flagged, nil
}

// effectiveSatisfiedImportance resolves the importance an upgrade candidate must
// BEAT for ch, and heals the satisfied_importance column when it has gone stale.
//
// satisfied_importance is a SNAPSHOT of the importance the chapter was satisfied
// at, so it goes stale the moment the owner re-ranks the sources. The truth, while
// the satisfying source is still attached, is that source's CURRENT importance:
//
//   - satisfied_by SET at a REAL importance (>= 1): the source's current importance
//     wins. When it differs from the stored snapshot the column is HEALED to it —
//     that is what unblocks a chapter whose satisfying source was DEMOTED (the
//     frozen, too-high snapshot otherwise out-ranks every real candidate and refuses
//     every upgrade, forever), and it converges the column on the next scan with no
//     backfill.
//   - satisfied_by SET at importance 0 — THE PARK SENTINEL: 0 is not a rank, it is
//     the marker library's Match/Dedup merge writes while it DB-parks a live provider
//     for the whole relabel window (see library.mergeDiskIntoLive / attachRealSource:
//     the no-redownload invariant is literally "0 <= any watermark, so DetectUpgrades
//     never fires"). Healing the watermark DOWN to 0 would defeat that park and let
//     any inferior sibling source (importance >= 1) out-rank the chapter and DOWNGRADE
//     it mid-merge. So a parked satisfier is treated as "no current importance" and the
//     FROZEN watermark guards. Safe to reserve: series.normalizeRanks emits multiples of
//     importanceStep (min 10), handler/library rejects importance < 1, and disk-origin
//     providers are importance 1 — no ranked provider is legitimately 0.
//   - satisfied_by NULL: the source was REMOVED by the owner (series.RemoveProvider
//     deliberately clears satisfied_by and KEEPS the watermark). There is no current
//     importance to read, so the FROZEN watermark still guards — it is precisely what
//     stops an equal-or-lower source from posing as an upgrade for a chapter already
//     satisfied at that quality. This fallback must not change.
//
// The caller must have eager-loaded the satisfied_by edge (detectUpgrades does so
// once for the whole scan — no per-chapter query). ch.SatisfiedImportance must be
// non-nil. A heal-write failure is LOGGED AND SKIPPED (the frozen watermark is used
// for this chapter, exactly as before the heal existed) — mirroring the neighbouring
// candidate-ranking failure, so one bad row-update never aborts the whole scan.
func effectiveSatisfiedImportance(ctx context.Context, client *ent.Client, ch *ent.Chapter) int {
	frozen := *ch.SatisfiedImportance

	// sp == nil also covers the defensive "id set but edge missing" case (a broken
	// FK): fall back to the frozen watermark rather than mis-ranking the chapter.
	sp := ch.Edges.SatisfiedBy
	if sp == nil || sp.Importance == 0 || sp.Importance == frozen {
		return frozen
	}

	if err := client.Chapter.UpdateOneID(ch.ID).
		SetSatisfiedImportance(sp.Importance).
		Exec(ctx); err != nil {
		slog.WarnContext(ctx, "download.DetectUpgrades: could not heal stale satisfied_importance — using the frozen watermark for this chapter",
			"chapter_id", ch.ID,
			"frozen_importance", frozen,
			"current_importance", sp.Importance,
			"err", err,
		)
		return frozen
	}
	return sp.Importance
}

// detectUpgradeForChapter evaluates a single chapter and transitions it to
// upgrade_available when a strictly higher-importance provider exists.
// Returns 1 if flagged, 0 if skipped or unchanged, and a non-nil error only
// for hard failures (state transition errors) that should abort the scan.
func detectUpgradeForChapter(ctx context.Context, client *ent.Client, gate *sourcegate.Service, ch *ent.Chapter, maxRetries int, now time.Time) (int, error) {
	// Defensive path: satisfied_importance should always be set for a downloaded
	// chapter (a successful download always writes it). Skip to avoid a nil-deref.
	if ch.SatisfiedImportance == nil {
		slog.WarnContext(ctx, "download.DetectUpgrades: downloaded chapter has nil satisfied_importance — skipping",
			"chapter_id", ch.ID,
			"chapter_key", ch.ChapterKey,
		)
		return 0, nil
	}

	// The bar an upgrade must beat — the satisfying source's CURRENT importance
	// (healing a stale snapshot), or the frozen watermark when that source was
	// removed or is PARKED at 0 by a library merge.
	effective := effectiveSatisfiedImportance(ctx, client, ch)

	best, err := bestUpgradeCandidate(ctx, client, gate, ch, maxRetries, now)
	if err != nil {
		// Log and continue — one chapter failing to scan should not abort all others.
		slog.WarnContext(ctx, "download.DetectUpgrades: failed to rank candidates for chapter — skipping",
			"chapter_id", ch.ID,
			"err", err,
		)
		return 0, nil
	}
	// No live, non-gated source offers this chapter right now — nothing to upgrade to.
	if best == nil {
		return 0, nil
	}

	// Self-churn guard: if the best live source IS the one that already satisfies
	// this chapter, an "upgrade" would re-fetch from the SAME source — pure churn.
	// Never flag, whatever its importance did (raising the CURRENT source's rank must
	// not re-fire an upgrade from that same source). The watermark is already healed
	// to its current importance by effectiveSatisfiedImportance above.
	if ch.SatisfiedByProviderID != nil && best.SeriesProvider.ID == *ch.SatisfiedByProviderID {
		return 0, nil
	}

	// Strict comparison: only flag when a DIFFERENT source is strictly higher than
	// the effective satisfied importance.
	if best.SeriesProvider.Importance <= effective {
		return 0, nil
	}

	if err := chapter.SetState(ctx, client, ch.ID, entchapter.StateUpgradeAvailable); err != nil {
		return 0, fmt.Errorf("download.DetectUpgrades: transition chapter %s to upgrade_available: %w", ch.ID, err)
	}
	return 1, nil
}

// bestUpgradeCandidate returns the highest-importance LIVE, NON-GATED source
// offering ch's chapter_key within the same series (attempts < maxRetries AND
// past per-source cooldown AND circuit-breaker not tripped), or nil when no
// eligible source exists. It reuses chapter.RankedLiveCandidates so the "live,
// importance-ranked" rule is defined once and is identical to the download path
// (§2 DRY), then applies the shared gate filter so a breaker-tripped higher
// source is never chosen as an upgrade target (nil gate never filters).
func bestUpgradeCandidate(ctx context.Context, client *ent.Client, gate *sourcegate.Service, ch *ent.Chapter, maxRetries int, now time.Time) (*chapter.Candidate, error) {
	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, maxRetries, now)
	if err != nil {
		return nil, fmt.Errorf("rank live candidates for chapter %s: %w", ch.ID, err)
	}
	cands = gateFilterCandidates(ctx, gate, cands, now)
	if len(cands) == 0 {
		return nil, nil
	}
	// RankedLiveCandidates is importance-DESC, so the first is the highest.
	return &cands[0], nil
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
		if errors.Is(err, errUpgradeNoLongerNeeded) {
			return d.finishStaleUpgrade(ctx, chapterID)
		}
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
	now := time.Now()
	cands, err := chapter.RankedLiveCandidates(ctx, d.client, chapterID, d.retry.MaxRetries(ctx), now)
	if err != nil {
		return upgradeResult{}, fmt.Errorf("rank live candidates: %w", err)
	}
	// Defense-in-depth: exclude sources cooled down by the politeness gate so a
	// blocked source is never fetched by the upgrade path even if a stale
	// upgrade_available flag survived (nil gate never filters).
	cands = d.filterGated(ctx, cands, now)
	if len(cands) == 0 {
		// Reachable when: DetectUpgrades flagged a chapter but the only better
		// source was then tripped/cooled/removed before this fetch (or a concurrent
		// owner action emptied it). Returning an error routes to handleUpgradeFailure
		// with a nil failedPC — the chapter transitions back to downloaded (working
		// copy intact), NOT stranded in upgrade_available.
		return upgradeResult{}, fmt.Errorf("no live source available for chapter %s", chapterID)
	}
	best := cands[0]

	// Defence-in-depth against a stale upgrade_available flag: if the best live
	// source IS the chapter's current satisfier, an upgrade would re-fetch from the
	// SAME source (the self-churn bug DetectUpgrades now prevents at the source).
	// Refresh the frozen satisfied_importance watermark to the source's current
	// importance and signal a clean no-op — no fetch, no re-render (mirrors the
	// len(cands)==0 early return, but without treating it as a failure).
	if ch.SatisfiedByProviderID != nil && best.SeriesProvider.ID == *ch.SatisfiedByProviderID {
		if err := d.client.Chapter.UpdateOneID(chapterID).
			SetSatisfiedImportance(best.SeriesProvider.Importance).
			Exec(ctx); err != nil {
			return upgradeResult{}, fmt.Errorf("refresh satisfied_importance: %w", err)
		}
		return upgradeResult{}, errUpgradeNoLongerNeeded
	}

	pc := best.ProviderChapter
	sp := best.SeriesProvider
	sourceKey := canonicalSourceKey(sp)

	// Carry a per-chapter progress sink so the upgrade fetch reports live per-page
	// progress too; the sink throttles + broadcasts download.progress ("upgrading").
	pctx := fetcher.WithProgress(ctx, d.progressSink(chapterID, string(entchapter.StateUpgrading)))
	// Politeness delay before the fetch (runtime-tunable per-source minimum gap).
	d.gateWait(pctx, sourceKey)
	release := limiter.acquire(sourceKey)
	pages, err := d.f.Fetch(pctx, buildFetchRef(pc, sp))
	release()
	if err != nil {
		// Circuit-breaker: a failed upgrade fetch is a "source down entirely"
		// signal, same as the download path. Skip on a shutdown-induced
		// cancellation (parent ctx done); a real per-fetch timeout still counts.
		if shouldRecordGateFailure(ctx) {
			d.gateRecordFailure(ctx, sourceKey, err, time.Now())
		}
		// Carry pc so handleUpgradeFailure bumps this source's per-source retry state.
		return upgradeResult{pc: pc, sp: sp}, err
	}
	// The fetch succeeded → the source is reachable; clear its breaker state.
	// (A later render/persist failure is not the source's fault, so it does not
	// touch the breaker.)
	d.gateRecordSuccess(ctx, sourceKey)

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

// tryDeleteOldCBZ performs a best-effort cleanup of superseded CBZs after a
// successful convergence. For a NUMBERED chapter it removes EVERY other CBZ in the
// series folder that shares this chapter's number — not just the single tracked
// old filename — keeping only newFilename (the new winning file). This converges
// the on-disk state to one file per chapter number: the previous winner AND any
// pre-existing duplicate provenance for the same chapter are cleaned up in one
// pass (disk.RemoveOtherChapterFiles). For an UN-numbered chapter (no number to
// match on) it falls back to removing just the single old filename when it changed.
//
// It resolves the series' REAL category folder via the shared seriesCategoryName
// (the same resolver buildRenderMeta uses to WRITE the file). Removal errors are
// logged, never fatal — a reconcile will clean up any straggler. ch is loaded
// WithSeries(WithCategory()) by Upgrade.
func (d *Dispatcher) tryDeleteOldCBZ(ctx context.Context, chapterID uuid.UUID, ch *ent.Chapter, newFilename string) {
	category := seriesCategoryName(ch)
	seriesTitle := ""
	if ch.Edges.Series != nil {
		seriesTitle = ch.Edges.Series.Title
	}

	if ch.Number != nil {
		removed, err := disk.RemoveOtherChapterFiles(d.cfg.Storage, category, seriesTitle,
			chapter.FormatChapterNumber(*ch.Number), newFilename)
		if err != nil {
			slog.WarnContext(ctx, "download.Dispatcher.Upgrade: best-effort duplicate-CBZ cleanup failed — a reconcile will clean it up",
				"chapter_id", chapterID,
				"err", err,
			)
		} else if removed > 0 {
			slog.InfoContext(ctx, "download.Dispatcher.Upgrade: removed superseded duplicate CBZs on convergence",
				"chapter_id", chapterID,
				"removed", removed,
			)
		}
		return
	}

	// Un-numbered chapter: no number to dedup by — remove just the old file if it changed.
	oldFilename := ch.Filename
	if oldFilename == "" || oldFilename == newFilename {
		return
	}
	oldPath := filepath.Join(disk.SeriesDir(d.cfg.Storage, category, seriesTitle), oldFilename)
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		slog.WarnContext(ctx, "download.Dispatcher.Upgrade: best-effort delete of old CBZ failed — a reconcile will clean it up",
			"chapter_id", chapterID,
			"old_path", oldPath,
			"err", err,
		)
	}
}

// finishStaleUpgrade cleanly resolves a stale upgrade_available flag whose best
// live source is the chapter's current satisfier (fetchAndRender returned
// errUpgradeNoLongerNeeded after already refreshing the watermark). It transitions
// upgrading → downloaded and broadcasts download.done — no fetch happened, the
// working copy is untouched, and NO last_error / upgrade.fail is recorded (this is
// not a failure). Always returns nil so callers treat it as a handled outcome.
func (d *Dispatcher) finishStaleUpgrade(ctx context.Context, chapterID uuid.UUID) error {
	if err := chapter.SetState(ctx, d.client, chapterID, entchapter.StateDownloaded); err != nil {
		// Defensive path: only reachable on a DB failure between the upgrading
		// transition and here. Log but return nil so the chapter is not stranded in
		// upgrading — the next DetectUpgrades run re-evaluates it.
		slog.ErrorContext(ctx, "download.Dispatcher.finishStaleUpgrade: could not transition upgrading→downloaded",
			"chapter_id", chapterID,
			"set_state_err", err,
		)
		return nil
	}
	d.broadcast("download.done", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateDownloaded),
	})
	return nil
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
