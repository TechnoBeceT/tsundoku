package downloads

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ent/predicate"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/pkg/errorclass"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// ErrChapterNotFound is returned by RetryChapter when no chapter matches the id.
// The HTTP handler maps it to a 404.
var ErrChapterNotFound = errors.New("chapter not found")

// ErrNotRetryable is returned by RetryChapter when the chapter is neither in a
// retryable state (failed / permanently_failed) NOR carries a chapter-specific
// per-source failure (a downloaded chapter whose upgrade source is failing). The
// HTTP handler maps it to a 409.
var ErrNotRetryable = errors.New("chapter is not in a retryable state")

// retryableStates is the set of chapter states a retry may reset to wanted. It is
// also the default scope of RetryAll when no explicit states are given.
var retryableStates = []entchapter.State{
	entchapter.StateFailed,
	entchapter.StatePermanentlyFailed,
}

// BreakerSnapshotter is the narrow read port over the source-politeness circuit-
// breaker: ONE batched snapshot of every source's breaker state, keyed by the
// canonical source name (breakerKey). *sourcegate.Service satisfies it. Attached via
// WithBreakers; nil (the default, e.g. in unit tests) skips the cooldown join and the
// waiting reason falls back to the persisted per-source backoff only.
type BreakerSnapshotter interface {
	Snapshot(ctx context.Context) (map[string]sourcegate.BreakerState, error)
}

// RetrySettings supplies the current per-source retry budget for the "N/max" badge,
// resolved AT USE-TIME so an owner's settings change applies on the next List (hot
// reload). *settings.Service and settings.Static both satisfy it (MaxRetries(ctx)).
// Attached via WithRetrySettings; nil reports MaxRetries as 0.
type RetrySettings interface {
	MaxRetries(ctx context.Context) int
}

// Service exposes the cross-library chapter-activity views and the owner retry
// actions. It owns the Ent client — all enrichment reuses the exported
// internal/series resolvers, so no importance/display logic is duplicated here —
// plus two OPTIONAL, nil-guarded read ports: the circuit-breaker snapshot (for the
// cooling_down waiting reason) and the retry settings (for the N/max badge). Both are
// attached via the With* builders so the ~20 existing NewService(client) call sites
// (and unit tests that need neither) are untouched.
type Service struct {
	client   *ent.Client
	breakers BreakerSnapshotter
	retry    RetrySettings
}

// NewService builds the downloads activity service over the given Ent client. The
// breaker-cooldown join and the retry-budget badge are opt-in — see WithBreakers /
// WithRetrySettings; without them List behaves exactly as before (backoff-only
// deferral, MaxRetries 0).
func NewService(client *ent.Client) *Service {
	return &Service{client: client}
}

// WithBreakers attaches the source-politeness breaker snapshot so List can surface
// the cooling_down waiting reason (the source-wide anti-ban cooldown a persisted-only
// deferral cannot see). Returns the receiver for chaining. A nil snapshotter is the
// no-op default.
func (s *Service) WithBreakers(b BreakerSnapshotter) *Service {
	s.breakers = b
	return s
}

// WithRetrySettings attaches the retry-budget accessor so each row can report the
// current MaxRetries alongside its per-source attempt count ("Comix · 2/3"). Returns
// the receiver for chaining. Nil reports MaxRetries as 0.
func (s *Service) WithRetrySettings(r RetrySettings) *Service {
	s.retry = r
	return s
}

// ListFilter selects and paginates a List call. States is the required set of
// chapter states to include (OR-matched). Query, when non-empty, restricts to
// series whose canonical title contains it (case-insensitive). Limit caps the
// page; Offset skips that many rows.
type ListFilter struct {
	States []entchapter.State
	Limit  int
	Offset int
	Query  string
	// IncludeSourceFailures widens the result to the HONEST FAILURES set (PART D):
	// in addition to the state filter, ANY chapter that has a ProviderChapter with
	// attempts>0 (a chapter-specific per-source failure) is included, regardless of
	// the chapter's own state (a DOWNLOADED chapter whose upgrade source keeps failing
	// is a failure too) or of source importance. Each such row's FailingProvider*/
	// Retryable/Terminal fields name that failing source. Default false = the plain
	// state-only view (existing behaviour, byte-for-byte).
	IncludeSourceFailures bool
}

// RetryAllFilter scopes a bulk RetryAll. States defaults to the retryable set
// (failed + permanently_failed) when empty. SeriesID, when set, restricts the
// reset to one series.
type RetryAllFilter struct {
	States   []entchapter.State
	SeriesID *uuid.UUID
	// IncludeSourceFailures also resets every chapter with a chapter-specific failing
	// source (ProviderChapter.attempts>0) that is NOT already covered by the state set —
	// e.g. a DOWNLOADED chapter whose upgrade source failed. Those chapters keep their
	// state (a downloaded chapter stays downloaded) but their failing sources get a fresh
	// budget so DetectUpgrades re-flags the upgrade. Default false = state-only reset.
	IncludeSourceFailures bool
}

// IsRetryableState reports whether a chapter in the given state may be reset to
// wanted by a retry. Exported so the handler can validate a retry-all state param
// against the exact same rule the service enforces (no drift, no duplication).
func IsRetryableState(st entchapter.State) bool {
	for _, r := range retryableStates {
		if st == r {
			return true
		}
	}
	return false
}

// List returns a paginated, state-filtered view of chapters across the whole
// library, each enriched with its series display title + cover and the resolved
// chapter name + provider.
//
// No-N+1: the query cost is a small constant, independent of page size. (1) one
// COUNT for the total; (2) one page query with WithSeries() (Ent batch-loads the
// distinct series in a single IN query); (3) ONE batch query for every provider
// of the page's distinct series, with their ProviderChapter feeds. The
// per-series name/display/cover/best-source values are then resolved once each,
// in memory, reusing the exported internal/series resolvers — never a query in a
// per-chapter or per-series loop.
func (s *Service) List(ctx context.Context, filter ListFilter) (DownloadListDTO, error) {
	preds := listPredicates(filter)

	total, err := s.client.Chapter.Query().Where(preds...).Count(ctx)
	if err != nil {
		return DownloadListDTO{}, fmt.Errorf("downloads.List: count chapters: %w", err)
	}

	rows, err := s.client.Chapter.Query().
		Where(preds...).
		Order(entchapter.ByNumber(), entchapter.ByChapterKey()).
		Limit(filter.Limit).
		Offset(filter.Offset).
		WithSeries(func(sq *ent.SeriesQuery) { sq.WithCategory() }).
		All(ctx)
	if err != nil {
		return DownloadListDTO{}, fmt.Errorf("downloads.List: query chapters: %w", err)
	}

	seriesByID, seriesIDs := distinctSeries(rows)
	provByID, provBySeries, err := s.loadProviders(ctx, seriesIDs)
	if err != nil {
		return DownloadListDTO{}, err
	}
	resolutions := resolveSeries(seriesByID, provBySeries)

	// One wall-clock read for the whole page so every row's waiting status is judged
	// against the same instant. The per-source retry budget and the breaker snapshot
	// are each read ONCE for the whole page (never per row): the snapshot is the
	// batched circuit-breaker join that closes the cooldown gap without an N+1.
	now := time.Now()
	maxRetries := s.maxRetries(ctx)
	breakerByKey := s.loadBreakers(ctx)

	items := make([]DownloadChapterDTO, len(rows))
	for i, ch := range rows {
		res := resolutions[ch.SeriesID]
		items[i] = newDownloadChapterDTO(
			ch,
			category.NameOf(seriesByID[ch.SeriesID]),
			res,
			resolveRow(ch, res, provByID, breakerByKey, maxRetries, now),
		)
	}
	return DownloadListDTO{Total: total, Items: items}, nil
}

// maxRetries reads the current per-source retry budget once (nil accessor → 0).
func (s *Service) maxRetries(ctx context.Context) int {
	if s.retry == nil {
		return 0
	}
	return s.retry.MaxRetries(ctx)
}

// loadBreakers fetches the whole circuit-breaker snapshot ONCE per List so the
// per-row cooldown join is a pure in-memory map lookup (no N+1). FAILS OPEN: the
// breaker join is advisory enrichment, so a snapshot read error (or no snapshotter
// attached) yields a nil map — the list still renders, waiting reasons just fall back
// to the persisted per-source backoff. Never lets breaker bookkeeping wedge the
// owner's #1 read model.
func (s *Service) loadBreakers(ctx context.Context) map[string]sourcegate.BreakerState {
	if s.breakers == nil {
		return nil
	}
	snap, err := s.breakers.Snapshot(ctx)
	if err != nil {
		slog.WarnContext(ctx, "downloads.List: breaker snapshot failed; skipping cooldown join", "err", err)
		return nil
	}
	return snap
}

// resolveRow computes the once-per-chapter enrichment the DTO mapper projects: the
// resolved source (id + label + its per-source attempt count), the upgrade marker +
// target, and the waiting status (reason + retry ETA + detail). Every value is read
// from data already in memory — the batch-loaded provider feeds and the single
// breaker snapshot — so it issues ZERO queries.
func resolveRow(ch *ent.Chapter, res seriesResolution, provByID map[uuid.UUID]*ent.SeriesProvider, breakerByKey map[string]sourcegate.BreakerState, maxRetries int, now time.Time) rowContext {
	sp, pc := chapterSource(ch, provByID, res.upgradeTargets)
	rc := rowContext{
		maxRetries: maxRetries,
		isUpgrade:  isUpgrading(ch.State),
	}
	// The upgrade TARGET's label AND its own per-source attempt count both come from
	// the single carrier pick, so the UI badges the source the chapter is converging
	// TO (the one actually fetched), not the satisfier it replaces — in memory over the
	// already-loaded feed index, no extra query.
	rc.upgradeTarget, rc.upgradeTargetAttempts = resolveUpgradeTarget(ch, res.upgradeTargets, provByID)
	if sp != nil {
		rc.provider = sp.Provider
		rc.providerName = series.ProviderLabel(sp)
	}
	if pc != nil {
		rc.attempts = pc.Attempts
	}

	// Honest failures (PART D): if any source has a chapter-specific failure on this
	// chapter, surface the highest-importance failing source with its N/max badge,
	// last_error + derived category, and retryable/terminal classification — for ANY
	// chapter state. A failing source on a DOWNLOADED chapter that is NOT its satisfier
	// is a broken UPGRADE fetch, so tag the row as an upgrade converging TO it (the
	// upgrade_available/upgrading states already carry their own target above).
	if fc, ok := failingCarrier(ch, res.upgradeTargets); ok {
		rc.failingProvider = fc.provider.Provider
		rc.failingProviderName = series.ProviderLabel(fc.provider)
		rc.failingAttempts = fc.pc.Attempts
		rc.failingLastError = fc.pc.LastError
		rc.failingErrorCategory = errorclass.ClassifyMessage(fc.pc.LastError)
		rc.retryable = fc.pc.Attempts < maxRetries
		rc.terminal = fc.pc.Attempts >= maxRetries
		if ch.State == entchapter.StateDownloaded && !isSatisfier(ch, fc.provider) {
			rc.isUpgrade = true
			rc.upgradeTarget = rc.failingProviderName
			// Keep upgradeTargetAttempts describing the source upgradeTarget names — here
			// the failing upgrade target, whose attempts are the failing count.
			rc.upgradeTargetAttempts = rc.failingAttempts
		}
	}

	// The waited-on source (upgrade target for an upgrade, primary candidate for a
	// wanted) carries BOTH cooldown signals: its persisted per-source backoff and its
	// source-wide circuit-breaker (joined from the snapshot by canonical name).
	waited, ok := waitedOnCarrier(ch, res.upgradeTargets, provByID)
	var breaker *sourcegate.BreakerState
	if ok && breakerByKey != nil {
		if b, found := breakerByKey[breakerKey(waited.provider)]; found {
			breaker = &b
		}
	}
	rc.waitingReason, rc.retryAt, rc.deferReason = waitingStatus(waited, ok, breaker, now)
	return rc
}

// listPredicates builds the Chapter predicates for List: the required state set
// (widened with the honest-failures OR when IncludeSourceFailures is set), plus
// (when a query is given) a series-title-contains filter via the series edge.
func listPredicates(filter ListFilter) []predicate.Chapter {
	main := entchapter.StateIn(filter.States...)
	if filter.IncludeSourceFailures {
		main = entchapter.Or(main, hasFailingSource())
	}
	preds := []predicate.Chapter{main}
	if filter.Query != "" {
		preds = append(preds, entchapter.HasSeriesWith(entseries.TitleContainsFold(filter.Query)))
	}
	return preds
}

// hasFailingSource is the correlated predicate behind the honest failures set: a
// chapter matches when ANY source offering its (series_id, chapter_key) has a
// per-source failure (ProviderChapter.attempts>0). There is no Ent edge from Chapter
// to ProviderChapter (they are linked structurally by series_id + chapter_key, not a
// FK), so this is an EXISTS subquery joining provider_chapters → series_providers and
// correlating on the outer chapter's series_id + chapter_key. It stays ONE predicate
// (ONE count query, ONE page query) — the failing-source resolution itself is then
// done in memory over the already-batch-loaded feeds (no N+1).
func hasFailingSource() predicate.Chapter {
	return predicate.Chapter(func(s *sql.Selector) {
		pc := sql.Table(entproviderchapter.Table)
		sp := sql.Table(entseriesprovider.Table)
		s.Where(sql.Exists(
			sql.Select().
				From(pc).
				Join(sp).On(pc.C(entproviderchapter.FieldSeriesProviderID), sp.C(entseriesprovider.FieldID)).
				Where(sql.And(
					sql.GT(pc.C(entproviderchapter.FieldAttempts), 0),
					sql.ColumnsEQ(sp.C(entseriesprovider.FieldSeriesID), s.C(entchapter.FieldSeriesID)),
					sql.ColumnsEQ(pc.C(entproviderchapter.FieldChapterKey), s.C(entchapter.FieldChapterKey)),
				)),
		))
	})
}

// distinctSeries collects the distinct series of a chapter page (from the
// eager-loaded Series edge), preserving first-seen order. The returned map is
// keyed by series id so per-chapter lookups never depend on Ent's edge pointer
// sharing.
func distinctSeries(rows []*ent.Chapter) (map[uuid.UUID]*ent.Series, []uuid.UUID) {
	byID := make(map[uuid.UUID]*ent.Series, len(rows))
	var ids []uuid.UUID
	for _, ch := range rows {
		if _, ok := byID[ch.SeriesID]; !ok {
			byID[ch.SeriesID] = ch.Edges.Series
			ids = append(ids, ch.SeriesID)
		}
	}
	return byID, ids
}

// loadProviders batch-loads every SeriesProvider (with its ProviderChapter feed)
// for the given series ids in ONE query, returning both a by-id index (for the
// satisfied-by provider lookup) and a by-series grouping (for name/display/source
// resolution). Returns empty maps when there are no ids.
func (s *Service) loadProviders(ctx context.Context, seriesIDs []uuid.UUID) (byID map[uuid.UUID]*ent.SeriesProvider, bySeries map[uuid.UUID][]*ent.SeriesProvider, err error) {
	byID = map[uuid.UUID]*ent.SeriesProvider{}
	bySeries = map[uuid.UUID][]*ent.SeriesProvider{}
	if len(seriesIDs) == 0 {
		return byID, bySeries, nil
	}
	providers, err := s.client.SeriesProvider.Query().
		Where(entseriesprovider.SeriesIDIn(seriesIDs...)).
		WithProviderChapters().
		All(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("downloads.List: load providers: %w", err)
	}
	for _, p := range providers {
		byID[p.ID] = p
		bySeries[p.SeriesID] = append(bySeries[p.SeriesID], p)
	}
	return byID, bySeries, nil
}

// resolveSeries computes the once-per-series enrichment for every series on the
// page, reusing the exported internal/series resolvers so the importance logic is
// defined exactly once (§2 DRY). It attaches each series' providers onto the node
// so MetadataProvider/SeriesDisplay can resolve the display name + cover the same
// way GetSeries does, and indexes their feeds once for the upgrade-target lookup.
func resolveSeries(seriesByID map[uuid.UUID]*ent.Series, provBySeries map[uuid.UUID][]*ent.SeriesProvider) map[uuid.UUID]seriesResolution {
	out := make(map[uuid.UUID]seriesResolution, len(seriesByID))
	for sid, row := range seriesByID {
		provs := provBySeries[sid]
		row.Edges.Providers = provs // reuse MetadataProvider/SeriesDisplay resolution
		displayName, coverURL := series.SeriesDisplay(row, series.MetadataProvider(row))
		out[sid] = seriesResolution{
			names:       series.ChapterTitles(provs),
			displayName: displayName,
			coverURL:    coverURL,
			// Built from the SAME eager-loaded feeds — no extra query (see
			// newUpgradeTargetIndex).
			upgradeTargets: newUpgradeTargetIndex(provs),
		}
	}
	return out
}

// Summary returns the global nav-badge counts — one row per relevant state — from a
// SINGLE grouped aggregate over the whole Chapter table (GROUP BY state), never a
// per-state round-trip. Downloading = in-flight; Queued = wanted (waiting to
// download; upgrade waves are background convergence, not counted here); Failed =
// failed + permanently_failed (both are "needs attention" and both are retryable).
// Cheap enough for a persistent nav badge polled on every screen.
func (s *Service) Summary(ctx context.Context) (DownloadSummaryDTO, error) {
	var rows []struct {
		State entchapter.State `json:"state"`
		Count int              `json:"count"`
	}
	err := s.client.Chapter.Query().
		GroupBy(entchapter.FieldState).
		Aggregate(ent.As(ent.Count(), "count")).
		Scan(ctx, &rows)
	if err != nil {
		return DownloadSummaryDTO{}, fmt.Errorf("downloads.Summary: aggregate chapter states: %w", err)
	}

	var out DownloadSummaryDTO
	for _, r := range rows {
		switch r.State {
		case entchapter.StateDownloading:
			out.Downloading += r.Count
		case entchapter.StateWanted:
			out.Queued += r.Count
		case entchapter.StateFailed, entchapter.StatePermanentlyFailed:
			out.Failed += r.Count
		}
	}
	return out, nil
}

// RetryChapter re-attempts one chapter, resetting the per-source retry state on
// every ProviderChapter offering it (attempts→0, last_error→"", next_attempt_at→null)
// so EVERY source — including the one being retried — gets a fresh budget (otherwise
// an exhausted source stays excluded and the retry silently does nothing). Two shapes,
// by the chapter's state:
//
//   - failed / permanently_failed → reset back to WANTED and clear the chapter's
//     failure bookkeeping (last_error, error_category, legacy retries→0,
//     next_attempt_at→null) so the next download cycle re-attempts it.
//   - any other state WITH a chapter-specific failing source (attempts>0) — a
//     DOWNLOADED chapter whose upgrade source keeps failing — keeps its state (its CBZ
//     is intact) but clears the chapter's last_error/error_category and resets the
//     failing sources, so DetectUpgrades re-flags the upgrade and it is re-attempted.
//
// It is a RESET, never a delete (the never-auto-delete invariant holds). Chapter +
// source resets run in one transaction so they can never half-apply. Returns
// ErrChapterNotFound (→404) for an unknown id, or ErrNotRetryable (→409) when the
// chapter is neither a retryable state nor carries a failing source.
func (s *Service) RetryChapter(ctx context.Context, id uuid.UUID) error {
	ch, err := s.client.Chapter.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrChapterNotFound
		}
		return fmt.Errorf("downloads.RetryChapter: load chapter %s: %w", id, err)
	}

	retryableState := IsRetryableState(ch.State)
	if !retryableState {
		hasFailing, err := chapterHasFailingSource(ctx, s.client, ch)
		if err != nil {
			return fmt.Errorf("downloads.RetryChapter: %w", err)
		}
		if !hasFailing {
			return ErrNotRetryable
		}
	}

	err = withTx(ctx, s.client, func(tx *ent.Tx) error {
		if retryableState {
			// failed/permanently_failed → wanted (the owner-retry edges); the field clears
			// accompany the transition in one update.
			if _, err := applyChapterRetryReset(tx.Chapter.Update().Where(entchapter.IDEQ(id))).Save(ctx); err != nil {
				return fmt.Errorf("reset chapter %s: %w", id, err)
			}
		} else {
			// A downloaded (etc.) chapter with a failing upgrade source: keep the state,
			// just clear the chapter's failure message so the retried source's fresh budget
			// takes over.
			if _, err := tx.Chapter.Update().Where(entchapter.IDEQ(id)).
				SetLastError("").SetErrorCategory("").Save(ctx); err != nil {
				return fmt.Errorf("clear chapter error %s: %w", id, err)
			}
		}
		return resetProviderChapters(ctx, tx, map[uuid.UUID][]string{ch.SeriesID: {ch.ChapterKey}})
	})
	if err != nil {
		return fmt.Errorf("downloads.RetryChapter: %w", err)
	}
	return nil
}

// chapterHasFailingSource reports whether any source offering ch's (series_id,
// chapter_key) has a chapter-specific per-source failure (ProviderChapter.attempts>0)
// — the single-chapter form of hasFailingSource, used by RetryChapter to admit a
// downloaded chapter whose upgrade source is failing.
func chapterHasFailingSource(ctx context.Context, client *ent.Client, ch *ent.Chapter) (bool, error) {
	n, err := client.ProviderChapter.Query().
		Where(
			entproviderchapter.ChapterKeyEQ(ch.ChapterKey),
			entproviderchapter.AttemptsGT(0),
			entproviderchapter.HasSeriesProviderWith(entseriesprovider.SeriesIDEQ(ch.SeriesID)),
		).
		Count(ctx)
	if err != nil {
		return false, fmt.Errorf("count failing sources for chapter %s: %w", ch.ID, err)
	}
	return n > 0, nil
}

// RetryAll bulk-resets every chapter in the filter's states back to wanted
// (clearing the same chapter + per-source failure state as RetryChapter),
// optionally scoped to one series, and returns how many chapters it reset. States
// defaults to the retryable set (failed + permanently_failed) when empty. The
// caller rejects non-retryable states (the handler validates via IsRetryableState);
// the service trusts the filter it is given. Chapter + source resets run in one
// transaction.
func (s *Service) RetryAll(ctx context.Context, filter RetryAllFilter) (int, error) {
	states := filter.States
	if len(states) == 0 {
		states = retryableStates
	}
	statePreds := retryAllStatePreds(filter, states)

	// Snapshot the state-failed chapters BEFORE the reset so the per-source reset
	// targets exactly the chapters being retried (they move to wanted).
	stateFailed, err := s.client.Chapter.Query().Where(statePreds...).All(ctx)
	if err != nil {
		return 0, fmt.Errorf("downloads.RetryAll: load target chapters: %w", err)
	}
	// Honest failures (PART D/E): chapters with a chapter-specific failing source not
	// already in the state set (e.g. a downloaded chapter whose upgrade source failed).
	// They keep their state; only their failing sources are reset.
	sourceFailed, err := s.loadSourceFailingForRetry(ctx, filter, states)
	if err != nil {
		return 0, fmt.Errorf("downloads.RetryAll: %w", err)
	}

	var n int
	err = withTx(ctx, s.client, func(tx *ent.Tx) error {
		return s.applyRetryAll(ctx, tx, statePreds, stateFailed, sourceFailed, &n)
	})
	if err != nil {
		return 0, fmt.Errorf("downloads.RetryAll: %w", err)
	}
	return n, nil
}

// retryAllStatePreds builds the state-failed predicate set (state IN states, plus the
// optional series scope) that RetryAll resets to wanted.
func retryAllStatePreds(filter RetryAllFilter, states []entchapter.State) []predicate.Chapter {
	preds := []predicate.Chapter{entchapter.StateIn(states...)}
	if filter.SeriesID != nil {
		preds = append(preds, entchapter.SeriesID(*filter.SeriesID))
	}
	return preds
}

// loadSourceFailingForRetry loads the chapters with a chapter-specific failing source
// that are NOT already in the state set (so RetryAll does not double-count / re-touch
// them), honouring the optional series scope. Nil when IncludeSourceFailures is off.
func (s *Service) loadSourceFailingForRetry(ctx context.Context, filter RetryAllFilter, states []entchapter.State) ([]*ent.Chapter, error) {
	if !filter.IncludeSourceFailures {
		return nil, nil
	}
	preds := []predicate.Chapter{hasFailingSource(), entchapter.Not(entchapter.StateIn(states...))}
	if filter.SeriesID != nil {
		preds = append(preds, entchapter.SeriesID(*filter.SeriesID))
	}
	chapters, err := s.client.Chapter.Query().Where(preds...).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("load source-failing chapters: %w", err)
	}
	return chapters, nil
}

// applyRetryAll performs the two RetryAll writes in one tx: state-failed chapters →
// wanted (+ field clears), source-failing chapters keep their state but clear their
// error, and BOTH groups' per-source retry state is reset. It writes the reset count
// into *n.
func (s *Service) applyRetryAll(ctx context.Context, tx *ent.Tx, statePreds []predicate.Chapter, stateFailed, sourceFailed []*ent.Chapter, n *int) error {
	reset, err := applyChapterRetryReset(tx.Chapter.Update().Where(statePreds...)).Save(ctx)
	if err != nil {
		return fmt.Errorf("reset chapters: %w", err)
	}
	*n = reset
	if len(sourceFailed) > 0 {
		if _, err := tx.Chapter.Update().
			Where(entchapter.IDIn(chapterIDs(sourceFailed)...)).
			SetLastError("").SetErrorCategory("").Save(ctx); err != nil {
			return fmt.Errorf("clear source-failing chapter errors: %w", err)
		}
		*n += len(sourceFailed)
	}
	return resetProviderChapters(ctx, tx, groupKeysBySeries(append(stateFailed, sourceFailed...)))
}

// chapterIDs projects a chapter slice to its ids.
func chapterIDs(chapters []*ent.Chapter) []uuid.UUID {
	ids := make([]uuid.UUID, len(chapters))
	for i, ch := range chapters {
		ids[i] = ch.ID
	}
	return ids
}

// applyChapterRetryReset applies the shared Chapter-side retry mutation: state→
// wanted plus the chapter's failure-field clears. Both RetryChapter (scoped to
// one id) and RetryAll (scoped to a state set) route through this single
// definition so the reset semantics can never diverge (§2 DRY). retries and
// next_attempt_at on the Chapter are legacy (the engine drives retry from
// ProviderChapter now) but are still cleared so no stale value lingers.
func applyChapterRetryReset(u *ent.ChapterUpdate) *ent.ChapterUpdate {
	return u.
		SetState(entchapter.StateWanted).
		SetRetries(0).
		SetLastError("").
		SetErrorCategory("").
		ClearNextAttemptAt()
}

// groupKeysBySeries collapses a set of chapters into a series_id → distinct
// chapter_keys map, the shape resetProviderChapters consumes so it can reset each
// series' ProviderChapter rows with a single precise predicate.
func groupKeysBySeries(chapters []*ent.Chapter) map[uuid.UUID][]string {
	out := make(map[uuid.UUID][]string, len(chapters))
	seen := make(map[uuid.UUID]map[string]struct{}, len(chapters))
	for _, ch := range chapters {
		if seen[ch.SeriesID] == nil {
			seen[ch.SeriesID] = map[string]struct{}{}
		}
		if _, dup := seen[ch.SeriesID][ch.ChapterKey]; dup {
			continue
		}
		seen[ch.SeriesID][ch.ChapterKey] = struct{}{}
		out[ch.SeriesID] = append(out[ch.SeriesID], ch.ChapterKey)
	}
	return out
}

// resetProviderChapters clears the per-source retry state (attempts→0,
// last_error→"", next_attempt_at→null) on every ProviderChapter that offers one
// of the given chapter_keys within its series, so a manual retry hands every
// source a fresh budget. It matches per (series, key) precisely — one bulk update
// per series — so a shared chapter_key across series never resets an unrelated
// series' sources.
func resetProviderChapters(ctx context.Context, tx *ent.Tx, bySeries map[uuid.UUID][]string) error {
	for seriesID, keys := range bySeries {
		if len(keys) == 0 {
			continue
		}
		if _, err := tx.ProviderChapter.Update().
			Where(
				entproviderchapter.ChapterKeyIn(keys...),
				entproviderchapter.HasSeriesProviderWith(entseriesprovider.SeriesIDEQ(seriesID)),
			).
			SetAttempts(0).
			SetLastError("").
			ClearNextAttemptAt().
			Save(ctx); err != nil {
			return fmt.Errorf("reset provider chapters for series %s: %w", seriesID, err)
		}
	}
	return nil
}

// withTx runs fn inside a database transaction, committing on success and rolling
// back (and joining any rollback error) on failure, so a multi-statement retry
// reset can never half-apply.
func withTx(ctx context.Context, client *ent.Client, fn func(tx *ent.Tx) error) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
