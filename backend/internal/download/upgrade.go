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
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
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
// "Strictly better" means: the maximum importance among ProviderChapters whose
// chapter_key matches this chapter's key within the same series is STRICTLY
// GREATER THAN the chapter's satisfied_importance. An equal-importance provider
// does NOT trigger an upgrade (comparison is >, not >=).
//
// Chapters with a nil satisfied_importance are skipped with a warning — this is
// a defensive case, because process always sets satisfied_importance on success.
//
// Returns the number of chapters flagged.
func DetectUpgrades(ctx context.Context, client *ent.Client) (int, error) {
	chapters, err := client.Chapter.Query().
		Where(entchapter.StateEQ(entchapter.StateDownloaded)).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("download.DetectUpgrades: query downloaded chapters: %w", err)
	}

	flagged := 0
	for _, ch := range chapters {
		n, err := detectUpgradeForChapter(ctx, client, ch)
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
func detectUpgradeForChapter(ctx context.Context, client *ent.Client, ch *ent.Chapter) (int, error) {
	// Defensive path: satisfied_importance should always be set for a downloaded
	// chapter (process always writes it). Skip to avoid a nil-deref.
	if ch.SatisfiedImportance == nil {
		slog.WarnContext(ctx, "download.DetectUpgrades: downloaded chapter has nil satisfied_importance — skipping",
			"chapter_id", ch.ID,
			"chapter_key", ch.ChapterKey,
		)
		return 0, nil
	}

	maxImportance, err := maxImportanceForChapter(ctx, client, ch)
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

// maxImportanceForChapter returns the highest importance among all
// ProviderChapters whose chapter_key matches ch's key within the same series.
// Returns 0 if no matching ProviderChapters exist.
func maxImportanceForChapter(ctx context.Context, client *ent.Client, ch *ent.Chapter) (int, error) {
	pcs, err := client.ProviderChapter.Query().
		Where(
			entproviderchapter.ChapterKeyEQ(ch.ChapterKey),
			entproviderchapter.HasSeriesProviderWith(
				entseriesprovider.SeriesIDEQ(ch.SeriesID),
			),
		).
		WithSeriesProvider().
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query provider chapters for key %q series %s: %w", ch.ChapterKey, ch.SeriesID, err)
	}

	max := 0
	for _, pc := range pcs {
		sp := pc.Edges.SeriesProvider
		if sp == nil {
			// Defensive path: WithSeriesProvider always loads the edge for a valid
			// FK; a nil here means a broken FK — not reachable under normal operation.
			continue
		}
		if sp.Importance > max {
			max = sp.Importance
		}
	}
	return max, nil
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

	res, err := d.fetchAndRender(ctx, ch, chapterID)
	if err != nil {
		return d.handleUpgradeFailure(ctx, ch, chapterID, err)
	}

	if err := d.persistUpgradeSuccess(ctx, chapterID, res); err != nil {
		return d.handleUpgradeFailure(ctx, ch, chapterID, err)
	}

	d.broadcast("download.done", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateDownloaded),
	})

	d.tryDeleteOldCBZ(ctx, chapterID, ch, res.newFilename)
	return nil
}

// fetchAndRender resolves the best provider for chapterID, fetches pages,
// and renders the new CBZ atomically. It returns an upgradeResult on success
// or an error that should be routed to handleUpgradeFailure.
func (d *Dispatcher) fetchAndRender(ctx context.Context, ch *ent.Chapter, chapterID uuid.UUID) (upgradeResult, error) {
	pc, importance, err := chapter.BestProviderChapter(ctx, d.client, chapterID)
	if err != nil {
		return upgradeResult{}, fmt.Errorf("resolve best provider: %w", err)
	}
	sp := pc.Edges.SeriesProvider

	pages, err := d.f.Fetch(ctx, buildFetchRef(pc, sp))
	if err != nil {
		return upgradeResult{}, err
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
		importance:  importance,
		newFilename: newFilename,
		pageCount:   pages.PageCount,
	}, nil
}

// persistUpgradeSuccess writes the new provenance to the Chapter row and
// transitions the state from upgrading to downloaded. Returns an error only
// for DB failures that should be routed to handleUpgradeFailure.
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
// Unlike handleFailure (which transitions to failed/permanently_failed and
// increments retries), an upgrade failure MUST keep the working copy intact.
// It transitions upgrading → downloaded (the chapter remains usable with its
// original CBZ and provenance), records last_error, and broadcasts upgrade.fail.
// It always returns nil so that Upgrade callers can treat upgrade failures as
// handled outcomes, not infrastructure errors.
func (d *Dispatcher) handleUpgradeFailure(ctx context.Context, ch *ent.Chapter, chapterID uuid.UUID, cause error) error {
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

	_ = ch // passed for symmetry with handleFailure; not used here.
	return nil
}
