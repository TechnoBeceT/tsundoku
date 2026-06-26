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
		items[i] = newDownloadChapterDTO(
			ch,
			category.NameOf(seriesByID[ch.SeriesID]),
			res,
			chapterProvider(ch, provByID, res.bestSource),
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
// way GetSeries does.
func resolveSeries(seriesByID map[uuid.UUID]*ent.Series, provBySeries map[uuid.UUID][]*ent.SeriesProvider) map[uuid.UUID]seriesResolution {
	out := make(map[uuid.UUID]seriesResolution, len(seriesByID))
	for sid, row := range seriesByID {
		provs := provBySeries[sid]
		row.Edges.Providers = provs // reuse MetadataProvider/SeriesDisplay resolution
		displayName, coverURL := series.SeriesDisplay(row, series.MetadataProvider(row))
		best := series.HighestImportanceProvider(provs)
		bestSource := ""
		if best != nil {
			bestSource = best.Provider
		}
		out[sid] = seriesResolution{
			names:       series.ChapterTitles(provs),
			displayName: displayName,
			coverURL:    coverURL,
			bestSource:  bestSource,
		}
	}
	return out
}

// chapterProvider resolves a chapter's provider key: the provider that satisfied
// it (satisfied_by_provider_id) when set and still present, else the series' top
// source (bestSource). A wanted/upgrade_available chapter has no satisfying
// source yet, so it shows the best available one.
func chapterProvider(ch *ent.Chapter, provByID map[uuid.UUID]*ent.SeriesProvider, bestSource string) string {
	if ch.SatisfiedByProviderID != nil {
		if p, ok := provByID[*ch.SatisfiedByProviderID]; ok {
			return p.Provider
		}
	}
	return bestSource
}

// RetryChapter resets one failed/permanently_failed chapter back to wanted so the
// next download cycle re-attempts it, clearing the failure bookkeeping
// (last_error, error_category, retries→0, next_attempt_at→null). It is a RESET,
// never a delete (the never-auto-delete invariant holds — a failed chapter has no
// CBZ). Returns ErrChapterNotFound (→404) for an unknown id, or ErrNotRetryable
// (→409) when the chapter is in a non-retryable state.
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
	// The state guard above makes failed/permanently_failed → wanted legal (the
	// owner-retry edges); the field clears accompany the transition in one update.
	if _, err := applyRetryReset(s.client.Chapter.Update().Where(entchapter.IDEQ(id))).Save(ctx); err != nil {
		return fmt.Errorf("downloads.RetryChapter: reset chapter %s: %w", id, err)
	}
	return nil
}

// RetryAll bulk-resets every chapter in the filter's states back to wanted
// (clearing the same failure fields as RetryChapter), optionally scoped to one
// series, and returns how many rows it reset. States defaults to the retryable
// set (failed + permanently_failed) when empty. The caller is responsible for
// rejecting non-retryable states (the handler validates via IsRetryableState);
// the service trusts the filter it is given.
func (s *Service) RetryAll(ctx context.Context, filter RetryAllFilter) (int, error) {
	states := filter.States
	if len(states) == 0 {
		states = retryableStates
	}
	preds := []predicate.Chapter{entchapter.StateIn(states...)}
	if filter.SeriesID != nil {
		preds = append(preds, entchapter.SeriesID(*filter.SeriesID))
	}
	n, err := applyRetryReset(s.client.Chapter.Update().Where(preds...)).Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("downloads.RetryAll: reset chapters: %w", err)
	}
	return n, nil
}

// applyRetryReset applies the shared retry mutation to a bulk Chapter update:
// state→wanted plus the failure-field clears. Both RetryChapter (scoped to one
// id) and RetryAll (scoped to a state set) route through this single definition
// so the reset semantics can never diverge between the two paths (§2 DRY).
func applyRetryReset(u *ent.ChapterUpdate) *ent.ChapterUpdate {
	return u.
		SetState(entchapter.StateWanted).
		SetRetries(0).
		SetLastError("").
		SetErrorCategory("").
		ClearNextAttemptAt()
}
