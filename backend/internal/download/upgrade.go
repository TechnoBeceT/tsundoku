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
	"golang.org/x/sync/semaphore"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entpredicate "github.com/technobecet/tsundoku/internal/ent/predicate"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// errUpgradeNoLongerNeeded signals that fetchAndRender found the chapter's
// current satisfier is still the best live source, so no fetch is warranted (a
// stale upgrade_available flag). It is NOT a failure: Upgrade returns the chapter
// to downloaded and refreshes the watermark in one ownership-aware transaction,
// without recording last_error or emitting upgrade.fail. This is the
// defence-in-depth partner to DetectUpgrades' self-churn guard.
var errUpgradeNoLongerNeeded = errors.New("upgrade no longer needed: current satisfier is already the best source")

// errUpgradeSourceUnavailable signals a stale upgrade marker whose previously
// selected target is no longer live. It is a handled no-fetch outcome, distinct
// from a local admission/state-transition error.
var errUpgradeSourceUnavailable = errors.New("upgrade source is no longer available")

// upgradeResult holds the artefacts produced by fetchAndRender so that
// Upgrade can persist them in a single update call.
type upgradeResult struct {
	owned       bool
	fetched     bool
	pc          *ent.ProviderChapter
	sp          *ent.SeriesProvider
	importance  int
	newFilename string
	pageCount   int
	// stagingDir is the on-disk page-staging directory the fetch used; the caller
	// deletes it after the upgraded CBZ is assembled (mirrors the download path's
	// cleanup) so the byte cache holds bytes only for in-progress chapters. "" when
	// no fetch staged pages (a stale/no-op upgrade).
	stagingDir string

	// usedCachedLinks reports whether the fetch drove its image loop from the page
	// links stored on pc rather than resolving them from the source. It is captured
	// BEFORE the fetch and handed to handleUpgradeFailure so a failed attempt on
	// EXPIRED cached links invalidates them (GAP-119) — see fetchAttempt.
	usedCachedLinks bool

	// refreshSatisfiedImportance is set only for the self-satisfier no-fetch
	// outcome. The ownership-aware stale-marker resolver writes it in the same
	// transaction that conditionally clears upgrade_available.
	refreshSatisfiedImportance *int
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
// the download path uses (chapter.RankedLiveCandidates). So a source that exhausted
// its budget — whether against the download path OR against repeated CHAPTER-SPECIFIC
// upgrade failures (an upgrade is a download: a broken-on-this-source chapter bumps
// attempts and eventually stops being flagged, ending the oscillation) — is never
// chosen as an upgrade target. A source merely on cooldown after a SOURCE-WIDE upgrade
// failure is skipped THIS cycle but re-considered once its cooldown elapses (a ban
// never spends budget, so a preferred source temporarily down always recovers as an
// upgrade target).
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
	return detectUpgrades(ctx, client, nil, maxRetries, nil)
}

// DetectUpgradesForSeries is the SERIES-SCOPED twin of DetectUpgrades (GAP-113):
// it evaluates only the downloaded chapters of ONE series, so a mutation that
// changes that series' candidate set (an adopt, a provider add/remove, a source
// re-rank) flags its freshly-upgradable chapters IMMEDIATELY instead of waiting for
// the next whole-library detection at the refresh cadence.
//
// It shares the exact batched, no-N+1 implementation as the whole-library scan
// (detectUpgradesScoped with a series filter) — same chapters.WithSatisfiedBy
// eager-load, the same chapter.RankedLiveCandidatesForMany batched candidate build
// (which, for a single-series input, loads only that series' feeds), the same
// once-per-scan breaker snapshot, and the byte-identical per-chapter decision
// (watermark heal, park-0/frozen carve-outs, self-churn guard, strict-higher, gate
// exclusion). So the set it flags for a series is identical to what the
// whole-library scan would flag for that same series. A nil gate makes it identical
// to the gate-free form.
func (d *Dispatcher) DetectUpgradesForSeries(ctx context.Context, seriesID uuid.UUID, maxRetries int) (int, error) {
	disabled, err := d.disabledSourceSet(ctx)
	if err != nil {
		return 0, fmt.Errorf("download.DetectUpgradesForSeries: %w", err)
	}
	return detectUpgradesScoped(ctx, d.client, d.gate, maxRetries, &seriesID, disabled)
}

// DetectUpgrades is the GATED form used in production. It excludes a source
// whose source-politeness circuit-breaker is tripped (cooled down) from being
// chosen as an upgrade target, so a Cloudflare-blocked higher-importance source
// is never FLAGGED for upgrade. This prevents an upgrade_available → upgrading →
// downloaded flag/revert flap every cycle while the source is down (the actual
// fetch would be blocked anyway by fetchAndRender's gate — this stops the churn
// at the source). A nil gate makes it identical to the package function.
func (d *Dispatcher) DetectUpgrades(ctx context.Context, maxRetries int) (int, error) {
	disabled, err := d.disabledSourceSet(ctx)
	if err != nil {
		return 0, fmt.Errorf("download.DetectUpgrades: %w", err)
	}
	return detectUpgrades(ctx, d.client, d.gate, maxRetries, disabled)
}

// detectUpgrades is the shared implementation behind both DetectUpgrades forms.
// gate may be nil (gate-free): every gated-candidate exclusion is a no-op then.
//
// It is NO-N+1 by construction (GAP-112): the whole downloaded set's live upgrade
// candidates are computed by ONE batched bulk load (chapter.RankedLiveCandidatesForMany
// — was a per-chapter chapter.RankedLiveCandidates = a ~30k-query N+1 on a large
// library, blocking every download cycle), and the source circuit-breaker is read
// ONCE for the whole scan (gate.Snapshot — was a per-candidate IsAvailable query).
// The only per-chapter DB touches left are the RARE stale-watermark heal-write and
// the SetState of a chapter that actually flags — both writes, both proportional to
// real work, not reads proportional to library size. The decision per chapter is
// byte-identical to the old per-chapter path.
func detectUpgrades(ctx context.Context, client *ent.Client, gate *sourcegate.Service, maxRetries int, disabled map[int64]bool) (int, error) {
	return detectUpgradesScoped(ctx, client, gate, maxRetries, nil, disabled)
}

// detectUpgradesScoped is the shared implementation behind the whole-library
// (seriesID nil) and per-series (seriesID set) detection. The only difference is a
// single added WHERE on series_id when scoped — everything downstream (the batched
// candidate build, the once-per-scan breaker snapshot, the per-chapter decision) is
// identical, so a scoped scan flags exactly the chapters of that series the
// whole-library scan would. See detectUpgrades for the no-N+1 rationale.
func detectUpgradesScoped(ctx context.Context, client *ent.Client, gate *sourcegate.Service, maxRetries int, seriesID *uuid.UUID, disabled map[int64]bool) (int, error) {
	now := time.Now()
	query := client.Chapter.Query().
		Where(entchapter.StateEQ(entchapter.StateDownloaded))
	if seriesID != nil {
		// Scoped: restrict the scan to one series' downloaded chapters. The batched
		// candidate load then loads only this series' feeds (RankedLiveCandidatesForMany
		// scopes to the distinct series of its input), so there is no N+1.
		query = query.Where(entchapter.SeriesID(*seriesID))
	}
	chapters, err := query.
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

	// Batch-compute every downloaded chapter's ranked live candidates in a bounded
	// number of queries — identical per-chapter result to RankedLiveCandidates.
	candsByChapter, err := chapter.RankedLiveCandidatesForMany(ctx, client, chapters, maxRetries, now, disabled)
	if err != nil {
		return 0, fmt.Errorf("download.DetectUpgrades: batch rank candidates: %w", err)
	}

	// Read the circuit-breaker snapshot ONCE for the whole scan; a per-candidate
	// gate query would reintroduce an N+1. A nil gate yields a nil snapshot ⇒ no
	// exclusion, exactly like the per-candidate nil-gate path.
	snap := loadBreakerSnapshot(ctx, gate)

	flagged := 0
	for _, ch := range chapters {
		n, err := detectUpgradeForChapter(ctx, client, ch, candsByChapter[ch.ID], snap, now)
		if err != nil {
			return flagged, err
		}
		flagged += n
	}
	return flagged, nil
}

// loadBreakerSnapshot reads every source's circuit-breaker state in ONE query for
// the whole upgrade scan (the batched twin of the per-candidate gate.IsAvailable
// used by the download path). A nil gate returns a nil map (no exclusion). A read
// error is LOGGED AND SWALLOWED, returning a nil map so detection proceeds WITHOUT
// gate exclusion — the same fail-OPEN direction as the per-candidate
// gate.IsAvailable (which returns "available" on a read error), and safe because
// the upgrade FETCH path (fetchAndRender.filterGated) re-checks the gate per source
// and cleanly resolves any stale upgrade_available flag it produced.
func loadBreakerSnapshot(ctx context.Context, gate *sourcegate.Service) map[string]sourcegate.BreakerState {
	if gate == nil {
		return nil
	}
	snap, err := gate.Snapshot(ctx)
	if err != nil {
		slog.WarnContext(ctx, "download.DetectUpgrades: breaker snapshot read failed — proceeding without gate exclusion (the upgrade fetch re-checks the gate per source)",
			"err", err,
		)
		return nil
	}
	return snap
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
//
// cands is ch's ranked live candidates, precomputed by the batched
// chapter.RankedLiveCandidatesForMany (byte-identical to RankedLiveCandidates);
// snap is the source circuit-breaker snapshot, read once for the whole scan (nil ⇒
// no gate exclusion). Neither costs a per-chapter query — that is the N+1 fix.
func detectUpgradeForChapter(ctx context.Context, client *ent.Client, ch *ent.Chapter, cands []chapter.Candidate, snap map[string]sourcegate.BreakerState, now time.Time) (int, error) {
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

	best := bestGatedCandidate(snap, cands, now)
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
//
// This is the SINGLE-CHAPTER form, used by the upgrade SCHEDULING pass
// (groupByUpgradeTarget) over the already-flagged upgrade_available set — a small,
// per-cycle-capped set, so its per-chapter query is not a scaling concern. The
// library-wide DETECTION scan uses the batched detectUpgrades path instead.
func bestUpgradeCandidate(ctx context.Context, client *ent.Client, gate *sourcegate.Service, ch *ent.Chapter, maxRetries int, now time.Time, disabled map[int64]bool) (*chapter.Candidate, error) {
	cands, err := chapter.RankedLiveCandidates(ctx, client, ch.ID, maxRetries, now, disabled)
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

// bestGatedCandidate is the snapshot-driven twin of bestUpgradeCandidate's tail:
// it applies the batched breaker-snapshot exclusion to an ALREADY-ranked candidate
// slice and returns the highest-importance survivor (cands is importance-DESC), or
// nil when none survive. Used by the batched detection scan so the gate is read
// once for the whole library rather than once per candidate.
func bestGatedCandidate(snap map[string]sourcegate.BreakerState, cands []chapter.Candidate, now time.Time) *chapter.Candidate {
	cands = gateFilterCandidatesSnapshot(snap, cands, now)
	if len(cands) == 0 {
		return nil
	}
	return &cands[0]
}

// gateFilterCandidatesSnapshot is the batched twin of gateFilterCandidates: it
// drops candidates whose physical source's circuit-breaker is COOLING DOWN, using
// a PRE-LOADED breaker snapshot (gate.Snapshot) instead of a per-source
// gate.IsAvailable query — so a library-wide scan reads the gate once, not once per
// candidate. It is byte-equivalent to gateFilterCandidates for the same DB state:
// gate.IsAvailable(key) is true unless a breaker row for key has a future cooldown,
// which is exactly BreakerState.IsCoolingDown; a source with no snapshot entry is
// available. A nil/empty snapshot filters nothing (identical to a nil gate).
func gateFilterCandidatesSnapshot(snap map[string]sourcegate.BreakerState, cands []chapter.Candidate, now time.Time) []chapter.Candidate {
	if len(snap) == 0 {
		return cands
	}
	out := make([]chapter.Candidate, 0, len(cands))
	for _, c := range cands {
		if st, ok := snap[canonicalSourceKey(c.SeriesProvider)]; ok && st.IsCoolingDown(now) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// Upgrade executes a non-destructive atomic upgrade for the given chapter.
//
// Flow (success path):
//  1. Load the chapter and resolve its best live provider.
//  2. Wait for source politeness, acquire fetch capacity, then transactionally
//     transition upgrade_available → upgrading and transfer ownership to the engine.
//  3. Fetch pages from the best provider and render the new CBZ atomically.
//  4. Persist updated provenance; clear last_error; transition upgrading → downloaded;
//     broadcast download.done.
//  5. Best-effort delete the old CBZ if the filename changed (different provider/scanlator
//     ⇒ different name); log on failure but do not fail the upgrade.
//
// Failure path (fetch or render error):
//   - Does NOT modify the existing file or its provenance.
//   - Transitions upgrading → downloaded (working copy retained).
//   - Records last_error; broadcasts upgrade.fail.
//   - Returns nil — an upgrade failure is a handled outcome, not a hard error.
//
// A cancellation or local state-write failure before engine ownership leaves the
// upgrade_available marker intact and returns an error; it is not a source failure.
func (d *Dispatcher) Upgrade(ctx context.Context, chapterID uuid.UUID) error {
	// Standalone single-chapter entry point: it owns its limiter, so nothing else
	// contends for it. UpgradeAll drives upgradeWith with ONE limiter shared across
	// the whole pass instead (see upgradeWith).
	policy, err := d.concurrencyPolicy(ctx)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.Upgrade: %w", err)
	}
	disabled, err := d.disabledSourceSet(ctx)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.Upgrade: %w", err)
	}
	_, err = d.upgradeWith(ctx, chapterID, newPolicyProviderLimiter(policy), disabled, nil)
	return err
}

// upgradeWith is Upgrade's body, parameterised by the per-provider fetch limiter so
// a batch of concurrent upgrades (UpgradeAll) can SHARE one — that is what caps the
// number of simultaneous fetches against any single physical source at
// DownloadConcurrency even if two chapters' upgrade targets resolve to the same
// source. completed reports whether this worker owned an engine attempt or stale
// marker resolution; a lost conditional claim returns false with no error. See
// Upgrade for the full flow + failure semantics.
func (d *Dispatcher) upgradeWith(ctx context.Context, chapterID uuid.UUID, limiter *providerLimiter, disabled map[int64]bool, globalSem *semaphore.Weighted) (completed bool, err error) {
	ch, err := d.client.Chapter.Query().
		Where(entchapter.IDEQ(chapterID)).
		WithSeries(func(sq *ent.SeriesQuery) { sq.WithCategory() }).
		Only(ctx)
	if err != nil {
		return false, fmt.Errorf("download.Dispatcher.Upgrade: load chapter %s: %w", chapterID, err)
	}

	// The upgrade fetch honours the SAME per-source concurrency cap as the download
	// path: the caller's limiter bounds concurrent fetches per physical source at
	// DownloadConcurrency. UpgradeAll passes ONE limiter for the whole pass, so its
	// per-source upgrade parallelism can never exceed that cap upstream.
	res, err := d.fetchAndRender(ctx, ch, chapterID, limiter, disabled, globalSem)
	if err != nil {
		if !res.fetched && (errors.Is(err, errUpgradeNoLongerNeeded) || errors.Is(err, errUpgradeSourceUnavailable)) {
			return d.finishUnstartedUpgrade(ctx, ch, res.refreshSatisfiedImportance)
		}
		if !res.fetched {
			// Admission and state-transition errors are local control failures: no
			// engine call happened, so keep upgrade_available and surface the error.
			return false, err
		}
		// A failed upgrade must NEVER reuse its partially-staged, index-keyed pages:
		// the next attempt re-resolves the page list fresh, and a reordered list
		// packed against those stale files would produce a mismatched CBZ that
		// tryDeleteOldCBZ then swaps in over the good original. Correctness over
		// resume — upgrades are infrequent — so wipe the target's staging dir on
		// every failure path (fetch, render, or persist). res.stagingDir is populated
		// on the fetch/render failure returns for exactly this.
		d.cleanupStaging(ctx, res.stagingDir)
		return true, d.handleUpgradeFailure(ctx, chapterID, res.pc, fetchAttempt{
			stagingDir:      res.stagingDir,
			usedCachedLinks: res.usedCachedLinks,
		}, err)
	}
	if !res.owned {
		return false, nil
	}

	if err := d.persistUpgradeSuccess(ctx, chapterID, res); err != nil {
		// A persist failure is a DB error, not the source's fault — no per-source
		// bump (failedPC is nil). Still wipe the staging dir (correctness over resume,
		// like every upgrade-failure path); the working copy on disk is untouched.
		d.cleanupStaging(ctx, res.stagingDir)
		return true, d.handleUpgradeFailure(ctx, chapterID, nil, fetchAttempt{}, err)
	}

	// The staged bytes are now inside the upgraded CBZ — delete the staging dir
	// (same self-cleaning lifecycle as the download path).
	d.cleanupStaging(ctx, res.stagingDir)

	d.broadcast("download.done", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateDownloaded),
	})

	d.tryDeleteOldCBZ(ctx, chapterID, ch, res.newFilename)
	return true, nil
}

// fetchAndRender resolves the best LIVE source for chapterID, fetches pages, and
// renders the new CBZ atomically. It returns an upgradeResult on success, or an
// error to route to handleUpgradeFailure. On a FETCH failure the returned result
// carries the attempted source's pc so the caller can CHARGE it with the same
// classified rule as the download path (classifyFetchFailure, all three of its
// kinds) — a chapter-specific upgrade failure BUMPS attempts (so the target
// exhausts and DetectUpgrades stops re-flagging it), a source-wide one only COOLS
// IT DOWN (so a preferred source temporarily down recovers as the swap target),
// and a DEFERRED one parks the target on a day-scale horizon with attempts
// UNCHANGED and no breaker (a better source withholding the chapter behind a
// paywall is not a fault, and the working copy on disk is untouched meanwhile —
// GAP-141). A render failure returns no pc (not the source's fault, so no charge).
func (d *Dispatcher) fetchAndRender(ctx context.Context, ch *ent.Chapter, chapterID uuid.UUID, limiter *providerLimiter, disabled map[int64]bool, globalSem *semaphore.Weighted) (upgradeResult, error) {
	now := time.Now()
	cands, err := chapter.RankedLiveCandidates(ctx, d.client, chapterID, d.retry.MaxRetries(ctx), now, disabled)
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
		// owner action emptied it). This handled no-fetch result resolves the stale
		// marker back to downloaded without entering source-failure accounting.
		return upgradeResult{}, fmt.Errorf("%w for chapter %s", errUpgradeSourceUnavailable, chapterID)
	}
	best := cands[0]

	// Defence-in-depth against a stale upgrade_available flag: if the best live
	// source IS the chapter's current satisfier, an upgrade would re-fetch from the
	// SAME source (the self-churn bug DetectUpgrades now prevents at the source).
	// Refresh the frozen satisfied_importance watermark to the source's current
	// importance and signal a clean no-op — no fetch, no re-render (mirrors the
	// len(cands)==0 early return, but without treating it as a failure).
	if ch.SatisfiedByProviderID != nil && best.SeriesProvider.ID == *ch.SatisfiedByProviderID {
		importance := best.SeriesProvider.Importance
		return upgradeResult{refreshSatisfiedImportance: &importance}, errUpgradeNoLongerNeeded
	}

	pc := best.ProviderChapter
	sp := best.SeriesProvider
	sourceKey := canonicalSourceKey(sp)

	// Carry a per-chapter progress sink so the upgrade fetch reports live per-page
	// progress too; the sink throttles + broadcasts download.progress ("upgrading").
	pctx := fetcher.WithProgress(ctx, d.progressSink(chapterID, string(entchapter.StateUpgrading)))
	// Read BEFORE the fetch: the upgrade target's stored links are what an expiry
	// makes stale, and only a pre-fetch read can distinguish "re-used" from
	// "resolved this attempt" (GAP-119; see fetchAttempt).
	usedCachedLinks := len(pc.PageLinks) > 0
	admission, err := d.fetchWithAdmission(pctx, sourceKey, buildFetchRef(pc, sp), limiter, globalSem, func() (bool, error) {
		guards := upgradeFrozenPredicates(ch)
		guards = append(guards, d.currentFetchCandidate(best, d.retry.MaxRetries(ctx), time.Now()))
		claimed, err := d.admitChapterFetch(
			ctx,
			chapterID,
			entchapter.StateUpgradeAvailable,
			entchapter.StateUpgrading,
			guards...,
		)
		if err != nil {
			return false, fmt.Errorf("download.Dispatcher.Upgrade: transition to upgrading for chapter %s: %w", chapterID, err)
		}
		if !claimed {
			return false, nil
		}
		d.broadcast("upgrade.start", DownloadEvent{ChapterID: chapterID, State: string(entchapter.StateUpgrading)})
		return true, nil
	})
	if err != nil {
		d.recordUpgradeFetchFailure(ctx, sourceKey, admission, err)
		// Carry pc so handleUpgradeFailure CHARGES this source's per-source retry state
		// with the SAME classified rule as the download path (chapter-specific → bump,
		// source-wide → cooldown), and stagingDir so the caller wipes the
		// partially-staged pages — Fetch populates StagingDir even on error. The fetched
		// flag keeps this metadata unreachable for a local admission error.
		return upgradeResult{fetched: admission.fetched, pc: pc, sp: sp, stagingDir: admission.pages.StagingDir, usedCachedLinks: usedCachedLinks}, err
	}
	if !admission.owned {
		return upgradeResult{}, nil
	}
	pages := admission.pages
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
		// A render failure is a LOCAL fault (no pc → no cooldown), but the fetch
		// already staged every page — carry stagingDir so the caller wipes it (a
		// failed upgrade never resumes, unlike the download path).
		return upgradeResult{owned: true, fetched: true, stagingDir: pages.StagingDir}, err
	}

	return upgradeResult{
		owned:       true,
		fetched:     true,
		pc:          pc,
		sp:          sp,
		importance:  sp.Importance,
		newFilename: newFilename,
		pageCount:   pages.PageCount,
		stagingDir:  pages.StagingDir,
	}, nil
}

// upgradeFrozenPredicates returns the provenance values an upgrade decision read
// before admission. The conditional claim must still match both values, including
// NULL, or it yields to the writer that changed the working copy's satisfier or
// frozen watermark.
func upgradeFrozenPredicates(ch *ent.Chapter) []entpredicate.Chapter {
	guards := make([]entpredicate.Chapter, 0, 2)
	if ch.SatisfiedByProviderID == nil {
		guards = append(guards, entchapter.SatisfiedByProviderIDIsNil())
	} else {
		guards = append(guards, entchapter.SatisfiedByProviderIDEQ(*ch.SatisfiedByProviderID))
	}
	if ch.SatisfiedImportance == nil {
		guards = append(guards, entchapter.SatisfiedImportanceIsNil())
	} else {
		guards = append(guards, entchapter.SatisfiedImportanceEQ(*ch.SatisfiedImportance))
	}
	return guards
}

// recordUpgradeFetchFailure updates the breaker only when the engine owned the
// attempt and returned a source-wide failure. Admission/state errors never reach
// source health accounting, and cancellation still suppresses breaker writes.
func (d *Dispatcher) recordUpgradeFetchFailure(ctx context.Context, sourceKey string, admission fetchAdmissionResult, cause error) {
	if !admission.fetched || !shouldRecordGateFailure(ctx, cause) {
		return
	}
	d.gateRecordFailure(ctx, sourceKey, cause, time.Now())
}

// finishUnstartedUpgrade conditionally owns and resolves a stale upgrade marker
// when no engine fetch is needed. It may clear only the exact
// upgrade_available/provenance snapshot the caller evaluated. A concurrent engine
// owner or completed cycle makes the conditional transition affect zero rows, so
// this worker yields without mutation, broadcast, or UpgradeAll progress.
func (d *Dispatcher) finishUnstartedUpgrade(ctx context.Context, ch *ent.Chapter, refreshImportance *int) (resolved bool, err error) {
	tx, err := d.client.Tx(ctx)
	if err != nil {
		return false, fmt.Errorf("download.Dispatcher.Upgrade: begin unstarted resolution for %s: %w", ch.ID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	resolved, err = chapter.TransitionIfCurrent(
		ctx,
		tx.Client(),
		ch.ID,
		entchapter.StateUpgradeAvailable,
		entchapter.StateDownloaded,
		upgradeFrozenPredicates(ch)...,
	)
	if err != nil || !resolved {
		return resolved, err
	}
	if refreshImportance != nil {
		if err := tx.Client().Chapter.UpdateOneID(ch.ID).
			SetSatisfiedImportance(*refreshImportance).
			Exec(ctx); err != nil {
			return false, fmt.Errorf("download.Dispatcher.Upgrade: refresh satisfied_importance for %s: %w", ch.ID, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("download.Dispatcher.Upgrade: commit unstarted resolution for %s: %w", ch.ID, err)
	}
	committed = true
	d.broadcast("download.done", DownloadEvent{ChapterID: ch.ID, State: string(entchapter.StateDownloaded)})
	return true, nil
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

// handleUpgradeFailure is the upgrade-specific failure handler.
//
// It keeps the working copy intact: it transitions upgrading → downloaded (the
// chapter stays usable with its original CBZ and provenance), records last_error,
// and broadcasts upgrade.fail. When the failure came from a fetch attempt (failedPC
// non-nil) it CHARGES the source with the SAME classified rule as the download path
// (chargeFetchFailure — an upgrade is a download): a CHAPTER-SPECIFIC upgrade
// failure BUMPS attempts (so the target exhausts at max_retries and DetectUpgrades
// stops re-flagging it, ending the perpetual downloaded↔upgrade_available
// oscillation), while a SOURCE-WIDE/ban upgrade failure only COOLS IT DOWN
// (attempts untouched — a preferred source temporarily down recovers as the swap
// target once it is back). A render/persist fault passes failedPC==nil, so it
// charges nothing (⑥) and attempt is ignored. It always returns nil so callers
// treat upgrade failures as handled outcomes, not infrastructure errors.
//
// attempt describes the fetch that failed. Its stagingDir has ALREADY been wiped by
// the caller (a failed upgrade never resumes); passing it on anyway makes
// chargeFetchFailure re-run the removal as a CHECK — a no-op when that wipe
// succeeded, and the guard that keeps the page links when it did not, so links and
// staged pages stay consistent on the upgrade path exactly as they do on the
// download path.
func (d *Dispatcher) handleUpgradeFailure(ctx context.Context, chapterID uuid.UUID, failedPC *ent.ProviderChapter, attempt fetchAttempt, cause error) error {
	if failedPC != nil {
		d.chargeFetchFailure(ctx, failedPC, attempt, cause, time.Now())
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
