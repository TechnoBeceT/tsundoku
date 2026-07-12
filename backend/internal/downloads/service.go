package downloads

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ent/predicate"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/series"
)

// ErrChapterNotFound is returned by RetryChapter when no chapter matches the id.
// The HTTP handler maps it to a 404.
var ErrChapterNotFound = errors.New("chapter not found")

// ErrNotRetryable is returned by RetryChapter when the chapter exists but is not
// in a retryable state (only failed and permanently_failed may be retried). The
// HTTP handler maps it to a 409.
var ErrNotRetryable = errors.New("chapter is not in a retryable state")

// retryableStates is the set of chapter states a retry may reset to wanted. It is
// also the default scope of RetryAll when no explicit states are given.
var retryableStates = []entchapter.State{
	entchapter.StateFailed,
	entchapter.StatePermanentlyFailed,
}

// Service exposes the cross-library chapter-activity views and the owner retry
// actions. It owns only the Ent client — all enrichment reuses the exported
// internal/series resolvers, so no importance/display logic is duplicated here.
type Service struct {
	client *ent.Client
}

// NewService builds the downloads activity service over the given Ent client.
func NewService(client *ent.Client) *Service {
	return &Service{client: client}
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
}

// RetryAllFilter scopes a bulk RetryAll. States defaults to the retryable set
// (failed + permanently_failed) when empty. SeriesID, when set, restricts the
// reset to one series.
type RetryAllFilter struct {
	States   []entchapter.State
	SeriesID *uuid.UUID
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

	items := make([]DownloadChapterDTO, len(rows))
	for i, ch := range rows {
		res := resolutions[ch.SeriesID]
		provID, provName := chapterProvider(ch, provByID, res.upgradeTargets)
		items[i] = newDownloadChapterDTO(
			ch,
			category.NameOf(seriesByID[ch.SeriesID]),
			res,
			provID,
			provName,
			upgradeTargetLabel(ch, res.upgradeTargets, provByID),
		)
	}
	return DownloadListDTO{Total: total, Items: items}, nil
}

// listPredicates builds the Chapter predicates for List: the required state set,
// plus (when a query is given) a series-title-contains filter via the series edge.
func listPredicates(filter ListFilter) []predicate.Chapter {
	preds := []predicate.Chapter{entchapter.StateIn(filter.States...)}
	if filter.Query != "" {
		preds = append(preds, entchapter.HasSeriesWith(entseries.TitleContainsFold(filter.Query)))
	}
	return preds
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

// chapterProvider resolves the source a chapter is ACTUALLY coming from, as
// (id, displayName). The id is SeriesProvider.provider (the raw source key); the
// name is series.ProviderLabel (display name, falling back to the id).
//
//  1. The provider that SATISFIED it (satisfied_by), when set and still present —
//     true provenance: this is where the file on disk came from.
//  2. Otherwise the highest-importance provider whose FEED CARRIES this chapter_key,
//     ranked exactly as the engine ranks candidates (importance DESC, then
//     ProviderChapter.ID ASC — see upgradeTargetIndex). That is the scheduler's own
//     primary-source rule (download/schedule.go groupBySource takes cands[0] of the
//     same importance-DESC, feed-bearing set), so the row names the source the engine
//     is really fetching from. Falling back to the series' TOP source instead — as
//     this used to — lies whenever the top source does not carry the key: in
//     production, chapters were labelled "Asura Scans" while the engine fetched them
//     from "Comic Asura", because Asura's feed has no such chapter. This case covers
//     every chapter nothing satisfies yet (downloading / wanted / failed) AND a
//     DOWNLOADED chapter whose satisfier was CLEARED — series.RemoveProvider nulls
//     satisfied_by by design (keeping the watermark and the CBZ), so such a row names
//     a remaining feed carrier rather than the source the file really came from. That
//     provenance is gone from the DB; the row answers "who would supply this chapter
//     now", which is what every other unsatisfied row answers too.
//  3. Otherwise "" — NO source carries this key, so nothing is fetching it. The
//     engine skips such a chapter every cycle (handleNoCandidates → download.skip,
//     stays wanted); reporting no source is the truth and surfaces it, where naming
//     the series' top source would repeat the very lie this fixes. A FRACTIONAL
//     chapter whose only carrier is a source the owner flagged ignore_fractional
//     lands here too — the index drops that source's fractional feed rows, exactly as
//     the engine drops it from candidacy — so the row correctly says nothing is
//     fetching it instead of naming the source it was told to ignore.
//
// GOTCHA (mirrors upgradeTargetLabel's): case 2 names the source the engine WOULD
// pick. The engine's STRUCTURAL exclusion — ignore_fractional — is mirrored by the
// index (see newUpgradeTargetIndex), because a permanently-excluded source must
// never be named. Its TRANSIENT ones are not: it also skips retry-exhausted /
// cooling-down / breaker-tripped sources, which this read model cannot see without
// the N+1 the feed index exists to avoid, and which clear on their own. It is a UI
// hint, never engine state.
func chapterProvider(ch *ent.Chapter, provByID map[uuid.UUID]*ent.SeriesProvider, idx upgradeTargetIndex) (id, name string) {
	if ch.SatisfiedByProviderID != nil {
		if p, ok := provByID[*ch.SatisfiedByProviderID]; ok {
			return p.Provider, series.ProviderLabel(p)
		}
	}
	if carriers := idx[ch.ChapterKey]; len(carriers) > 0 {
		return carriers[0].provider.Provider, series.ProviderLabel(carriers[0].provider)
	}
	return "", ""
}

// RetryChapter resets one failed/permanently_failed chapter back to wanted so the
// next download cycle re-attempts it. It clears the chapter's failure bookkeeping
// (last_error, error_category, legacy retries→0, next_attempt_at→null) AND resets
// the per-source retry state on every ProviderChapter offering this chapter
// (attempts→0, last_error→"", next_attempt_at→null) so EVERY source gets a fresh
// budget — otherwise a source that had exhausted its attempts would still be
// excluded and the retry would silently do nothing. It is a RESET, never a delete
// (the never-auto-delete invariant holds — a failed chapter has no CBZ). Chapter +
// source resets run in one transaction so they can never half-apply. Returns
// ErrChapterNotFound (→404) for an unknown id, or ErrNotRetryable (→409) when the
// chapter is in a non-retryable state.
func (s *Service) RetryChapter(ctx context.Context, id uuid.UUID) error {
	ch, err := s.client.Chapter.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrChapterNotFound
		}
		return fmt.Errorf("downloads.RetryChapter: load chapter %s: %w", id, err)
	}
	if !IsRetryableState(ch.State) {
		return ErrNotRetryable
	}

	err = withTx(ctx, s.client, func(tx *ent.Tx) error {
		// The state guard above makes failed/permanently_failed → wanted legal (the
		// owner-retry edges); the field clears accompany the transition in one update.
		if _, err := applyChapterRetryReset(tx.Chapter.Update().Where(entchapter.IDEQ(id))).Save(ctx); err != nil {
			return fmt.Errorf("reset chapter %s: %w", id, err)
		}
		return resetProviderChapters(ctx, tx, map[uuid.UUID][]string{ch.SeriesID: {ch.ChapterKey}})
	})
	if err != nil {
		return fmt.Errorf("downloads.RetryChapter: %w", err)
	}
	return nil
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
	preds := []predicate.Chapter{entchapter.StateIn(states...)}
	if filter.SeriesID != nil {
		preds = append(preds, entchapter.SeriesID(*filter.SeriesID))
	}

	// Snapshot the affected (series, chapter_key) pairs BEFORE the reset so the
	// per-source reset targets exactly the chapters being retried.
	affected, err := s.client.Chapter.Query().Where(preds...).All(ctx)
	if err != nil {
		return 0, fmt.Errorf("downloads.RetryAll: load target chapters: %w", err)
	}

	var n int
	err = withTx(ctx, s.client, func(tx *ent.Tx) error {
		reset, err := applyChapterRetryReset(tx.Chapter.Update().Where(preds...)).Save(ctx)
		if err != nil {
			return fmt.Errorf("reset chapters: %w", err)
		}
		n = reset
		return resetProviderChapters(ctx, tx, groupKeysBySeries(affected))
	})
	if err != nil {
		return 0, fmt.Errorf("downloads.RetryAll: %w", err)
	}
	return n, nil
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
