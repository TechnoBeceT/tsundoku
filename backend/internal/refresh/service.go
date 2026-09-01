// Package refresh implements the M5 discovery sweep: the recurring poll that
// re-fetches every monitored series' chapter list across all its providers to
// discover new releases. It is pure orchestration over the engine-agnostic
// ingest engine (internal/ingest) — it invents no new data mapping.
//
// The sweep is UPSERT-ONLY (it reuses ingest.Ingest.AddSeriesWithChapters) so
// it honors the never-auto-delete invariant: a chapter that disappears from a
// source listing on a later poll leaves its ProviderChapter row (and any
// rendered CBZ) untouched. Re-fetch never resets SeriesProvider.importance —
// only the create path sets it — so an owner re-rank survives every
// subsequent sweep.
package refresh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entsuwayomisyncstate "github.com/technobecet/tsundoku/internal/ent/suwayomisyncstate"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/provideraddress"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// Concurrency supplies the runtime-tunable parallel-refetch bound. RefreshAll
// reads it at the START of each sweep so an owner's change via the settings API
// applies to the next sweep without a restart. *settings.Service and
// settings.Static both satisfy it.
type Concurrency interface {
	// RefreshConcurrency is the maximum number of provider re-fetches that may run
	// in parallel in one sweep.
	RefreshConcurrency(ctx context.Context) int
}

// Service runs the discovery sweep. Create one with NewService and call
// RefreshAll on a schedule (job.Runner.StartRefresh) or on demand.
type Service struct {
	client      *ent.Client
	ingest      *ingest.Ingest
	hub         *sse.Hub
	concurrency Concurrency
	gate        *sourcegate.Service
	// events is the nil-guarded source-operation audit-log recorder. When
	// attached (WithEventRecorder), each sweep logs one `refresh` event per
	// source-manga group (the actual upstream fetch unit), batched and flushed
	// once after the sweep. Nil ⇒ no audit events (existing call sites unaffected).
	events sourceevents.Recorder
	// disabled is the nil-guarded owner-paused-source store (QCAT-513). When
	// attached (WithDisabledSources), every sweep reads the paused set ONCE and
	// skips those sources' providers entirely — no upstream fetch, no sync-state
	// write, no error counted. Nil ⇒ nothing is paused (the pre-QCAT-513
	// behaviour), so existing call sites and tests are unaffected.
	disabled DisabledSources
}

// DisabledSources is the narrow read surface the sweep needs to honour the
// owner's per-source TEMPORARY PAUSE (QCAT-513): the set of engine source ids
// that are currently paused (a row's presence = paused). *disabledsource.Service
// satisfies it.
//
// Skipping is the whole point — a paused source is one the owner has established
// cannot be fetched right now (a CAPTCHA wall, an indefinite outage), so
// re-polling it every sweep is pure churn against a source that will not answer.
// The skip DELETES NOTHING: the provider row, its feed and its CBZs are all left
// exactly as they are, and un-pausing resumes discovery on the very next sweep.
type DisabledSources interface {
	// Disabled returns the currently-paused engine source ids.
	Disabled(ctx context.Context) (map[int64]bool, error)
}

// WithEventRecorder attaches the source-operation audit-log recorder so each
// sweep logs a `refresh` event per source-manga group. It returns the receiver
// for chaining off NewService. A nil recorder logs nothing (best-effort — never
// affects the sweep).
func (s *Service) WithEventRecorder(r sourceevents.Recorder) *Service {
	s.events = r
	return s
}

// WithDisabledSources attaches the owner's per-source pause store (QCAT-513) so
// the sweep skips a paused source's providers instead of re-fetching them. It
// returns the receiver for chaining off NewService, mirroring WithEventRecorder.
//
// Passing nil (or never calling this) leaves the sweep exactly as it was before
// the pause existed. It is a chaining setter rather than a NewService parameter
// for the same reason WithEventRecorder is: an optional collaborator whose
// absence means "today's behaviour" does not belong in the required signature,
// and every existing construction stays valid.
func (s *Service) WithDisabledSources(d DisabledSources) *Service {
	s.disabled = d
	return s
}

// disabledSourceSet reads the owner's paused-source set ONCE for a sweep. A nil
// store (nothing wired) yields a nil map, which buildRefreshGroups reads as
// "nothing is paused".
//
// A read failure is LOGGED and treated as "nothing is paused" rather than
// aborting the sweep: discovery for every OTHER source is worth more than a
// perfectly-honoured pause for one, and the paused source's own fetch failures
// are already bounded by the politeness gate. The log line is what keeps this
// from being a silent degradation.
func (s *Service) disabledSourceSet(ctx context.Context) map[int64]bool {
	if s.disabled == nil {
		return nil
	}
	set, err := s.disabled.Disabled(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "refresh: could not read paused sources — sweeping every source this cycle", "err", err)
		return nil
	}
	return set
}

// NewService constructs a Service. ingestSvc is refresh's OWN ingest instance —
// production wires it with a PRIVATE ChapterCache (see cmd/tsundoku/main.go):
// refresh never reads that cache (it always fetches fresh via
// FetchChaptersUncached), so a private instance keeps this slice from touching
// the SHARED cache other interactive callers use. concurrency supplies the
// runtime-tunable parallel-refetch bound, read at the start of every sweep
// (hot reload). gate is the source-politeness circuit-breaker + delay
// (internal/sourcegate), consulted per provider before re-fetching it — see
// RefreshAll. gate may be nil (no gate configured): every gate-consulting call
// site treats a nil gate as "always available, no delay" (today's
// pre-politeness behaviour), so passing nil is a safe default for callers that
// do not need the gate.
func NewService(client *ent.Client, ingestSvc *ingest.Ingest, hub *sse.Hub, concurrency Concurrency, gate *sourcegate.Service) *Service {
	return &Service{client: client, ingest: ingestSvc, hub: hub, concurrency: concurrency, gate: gate}
}

// RefreshResult summarises one sweep. SeriesRefreshed counts the monitored
// series considered; ProvidersRefreshed counts providers successfully
// re-fetched; NewChapters sums genuinely-new Chapter rows created; Errors counts
// providers whose re-fetch failed (and were skipped — the sweep continues).
type RefreshResult struct {
	SeriesRefreshed    int
	ProvidersRefreshed int
	NewChapters        int
	Errors             int
}

// RefreshAll sweeps every monitored series. For each of its providers (with a
// numeric, URL-addressed source) it re-runs ingest.Ingest.AddSeriesWithChapters
// under bounded concurrency, discovering new chapters. Per-provider failures
// are logged and skipped (partial success). A hard error is returned only if
// the initial monitored-series query fails. Emits refresh.start before and
// refresh.done after the sweep.
func (s *Service) RefreshAll(ctx context.Context) (RefreshResult, error) {
	seriesList, err := s.client.Series.Query().
		// Skip completed series: a finished series has no new chapters, so polling
		// it is wasted work (and would freeze its sync state into false staleness).
		Where(entseries.Monitored(true), entseries.Completed(false)).
		WithProviders().
		All(ctx)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("refresh.RefreshAll: query monitored series: %w", err)
	}
	return s.sweep(ctx, seriesList), nil
}

// RefreshSeries is the per-series scoped twin of RefreshAll (GAP-113): it
// re-fetches only ONE series' provider feeds so a mutation that creates upgrade
// candidates (an adopt, a provider add/change/remove) discovers new chapters for
// THAT series immediately instead of waiting for the next whole-library sweep. It
// reuses the exact same sweep body — group by (physical source, manga), fetch each
// once under bounded concurrency, ingest every scanlator-provider from the shared
// raw list — so it is UPSERT-ONLY (never deletes a ProviderChapter row or a CBZ,
// Rule 2) and stamps last_synced_at/last_error per provider identically.
//
// It honors the SAME monitored/completed gate as RefreshAll: a non-monitored or
// completed series is excluded from the whole-library sweep, so the scoped path
// skips it too (a no-op zero result, no upstream fetch). A series that was deleted
// concurrently (not found) is likewise a no-op. Emits refresh.start/refresh.done
// around the fetch, reusing the whole-library event shape (the FE already handles
// these names).
func (s *Service) RefreshSeries(ctx context.Context, seriesID uuid.UUID) (RefreshResult, error) {
	sr, err := s.client.Series.Query().
		Where(entseries.IDEQ(seriesID)).
		WithProviders().
		Only(ctx)
	if ent.IsNotFound(err) {
		// Deleted between the mutation and this scoped refresh — nothing to do.
		return RefreshResult{}, nil
	}
	if err != nil {
		return RefreshResult{}, fmt.Errorf("refresh.RefreshSeries: query series %s: %w", seriesID, err)
	}
	// Same gate RefreshAll applies via its query filter: a completed or unmonitored
	// series is never swept, so the scoped path is a no-op for it too.
	if !sr.Monitored || sr.Completed {
		return RefreshResult{}, nil
	}
	return s.sweep(ctx, []*ent.Series{sr}), nil
}

// sweep is the shared body of RefreshAll (whole library) and RefreshSeries (one
// series): it groups every provider in seriesList by its (physical source, manga)
// so each source-manga's chapter list is fetched ONCE and every scanlator-provider
// that shares it is ingested from that single result (de-amplification — a series
// followed under three scanlators of the same source triggers one FetchChapters,
// not three). Fetches run under the runtime-tunable concurrency bound, per-provider
// failures are logged and skipped (partial success). A source-aware admission
// queue keeps a pending source represented in the active set before spare slots
// are filled round-robin, so one wedged source cannot occupy every slot while
// another source can still run.
// refresh.start/refresh.done bracket the sweep. It is UPSERT-ONLY, so a chapter
// that vanished from a source listing keeps its row and CBZ (Rule 2).
func (s *Service) sweep(ctx context.Context, seriesList []*ent.Series) RefreshResult {
	s.broadcast("refresh.start", RefreshEvent{Monitored: len(seriesList)})

	now := time.Now()
	groups := s.buildRefreshGroups(ctx, seriesList, now)

	var mu sync.Mutex
	result := RefreshResult{SeriesRefreshed: len(seriesList)}
	// sink collects one `refresh` audit event per group, flushed ONCE after the
	// sweep (nil when no recorder is wired, so nothing is collected).
	sink := s.newEventSink()

	// Read the parallel-refetch bound at use-time so a settings change applies to
	// this sweep. The bound caps concurrent GROUPS (each = one upstream fetch)
	// rather than providers.
	s.runRefreshGroups(ctx, groups, s.refreshLimit(ctx), func(gctx context.Context, grp refreshGroup) {
		s.refreshGroup(gctx, grp, now, &mu, &result, sink)
	})
	s.flushEventSink(ctx, sink)

	s.broadcast("refresh.done", RefreshEvent{
		Monitored:          len(seriesList),
		SeriesRefreshed:    result.SeriesRefreshed,
		ProvidersRefreshed: result.ProvidersRefreshed,
		NewChapters:        result.NewChapters,
		Errors:             result.Errors,
	})
	return result
}

type refreshSourceQueue struct {
	groups   []refreshGroup
	next     int
	inFlight int
}

// refreshAdmissionQueue retains discovery order within each source and rotates
// among sources. next prioritises a source with pending work and no active group;
// only after every pending source is represented may spare global capacity admit
// a second group from one source.
type refreshAdmissionQueue struct {
	bySource map[int64]*refreshSourceQueue
	sources  []*refreshSourceQueue
	cursor   int
}

func newRefreshAdmissionQueue(groups []refreshGroup) *refreshAdmissionQueue {
	queue := &refreshAdmissionQueue{bySource: make(map[int64]*refreshSourceQueue)}
	for _, grp := range groups {
		source, ok := queue.bySource[grp.sourceID]
		if !ok {
			source = &refreshSourceQueue{}
			queue.bySource[grp.sourceID] = source
			queue.sources = append(queue.sources, source)
		}
		source.groups = append(source.groups, grp)
	}
	return queue
}

func (q *refreshAdmissionQueue) next() (refreshGroup, bool) {
	for _, requireIdle := range []bool{true, false} {
		for offset := range len(q.sources) {
			index := (q.cursor + offset) % len(q.sources)
			source := q.sources[index]
			if source.next >= len(source.groups) || (requireIdle && source.inFlight > 0) {
				continue
			}
			grp := source.groups[source.next]
			source.next++
			source.inFlight++
			q.cursor = (index + 1) % len(q.sources)
			return grp, true
		}
	}
	return refreshGroup{}, false
}

func (q *refreshAdmissionQueue) complete(sourceID int64) {
	q.bySource[sourceID].inFlight--
}

// runRefreshGroups is a work-conserving source-aware scheduler. It starts no
// more than limit groups, waits for completions, and immediately reuses freed
// capacity. A pending source with no active work receives the next slot before
// any already-active source can expand, preventing a wedged source from owning
// the whole global allowance while retaining full throughput when fewer sources
// have work.
func (s *Service) runRefreshGroups(ctx context.Context, groups []refreshGroup, limit int, run func(context.Context, refreshGroup)) {
	queue := newRefreshAdmissionQueue(groups)
	g, gctx := errgroup.WithContext(ctx)
	completed := make(chan int64, limit)
	running := 0

	for {
		for running < limit {
			grp, ok := queue.next()
			if !ok {
				break
			}
			running++
			g.Go(func() error {
				defer func() { completed <- grp.sourceID }()
				run(gctx, grp)
				return nil
			})
		}
		if running == 0 {
			break
		}
		sourceID := <-completed
		running--
		queue.complete(sourceID)
	}

	// Every started group reports completion before the loop exits. Wait retains the
	// errgroup join contract and keeps a parent cancellation visible to workers.
	_ = g.Wait()
}

// refreshLimit resolves the runtime-tunable parallel-refetch bound at use-time,
// clamped to >= 1 (a 0 limit would deadlock the errgroup).
func (s *Service) refreshLimit(ctx context.Context) int {
	if limit := s.concurrency.RefreshConcurrency(ctx); limit >= 1 {
		return limit
	}
	return 1
}

// refreshProvider is one scanlator-provider queued for re-ingest within a group.
type refreshProvider struct {
	title      string
	provider   string
	providerID uuid.UUID
	// scanlator is the STORED scanlator of this SeriesProvider row (set at
	// create time — see ingest.Ingest.upsertSeriesProvider). It MUST be
	// passed back into AddSeriesWithChapters so a re-ingest updates this SAME
	// row instead of find-or-creating a fresh scanlator=="" one: ingest keys
	// SeriesProvider on (series, provider, scanlator), and a mismatched
	// scanlator here would silently split one provider into two.
	scanlator string
}

// refreshGroup is every provider that shares ONE (physical source, manga URL):
// they are satisfied by a single upstream Chapters call, then ingested per
// scanlator.
type refreshGroup struct {
	sourceID  int64
	url       string
	ref       sourceengine.ProviderRef
	sourceKey string
	providers []refreshProvider
}

// buildRefreshGroups flattens every monitored series' providers into groups keyed
// by (numeric source id, manga url). Which providers are worth fetching at all —
// disk-origin rows, rows with no URL, and rows whose source the owner has PAUSED
// (QCAT-513) — is decided by fetchableSourceID. A whole group whose physical
// source is currently cooled down by the source-politeness gate is then dropped
// (a tripped source is excluded from the sweep entirely this cycle, mirroring the
// download dispatcher's candidacy exclusion). Extracted from RefreshAll to keep
// its cyclomatic complexity low.
//
// The paused set is read ONCE here for the whole sweep, never per provider: a
// library-wide sweep walks every provider of every monitored series, so a
// per-provider read would be a straight N+1 on the hottest loop in the package.
func (s *Service) buildRefreshGroups(ctx context.Context, seriesList []*ent.Series, now time.Time) []refreshGroup {
	type key struct {
		source      int64
		url         string
		addressMode sourceengine.AddressMode
		webURL      string
	}
	disabled := s.disabledSourceSet(ctx)
	byKey := make(map[key]*refreshGroup)
	var order []key
	for _, sr := range seriesList {
		for _, p := range sr.Edges.Providers {
			sourceID, ok := s.fetchableSourceID(ctx, sr, p, disabled)
			if !ok {
				continue
			}
			mode := provideraddress.FromStored(p.AddressMode)
			k := key{source: sourceID, url: p.URL, addressMode: mode, webURL: p.WebURL}
			grp, ok := byKey[k]
			if !ok {
				grp = &refreshGroup{
					sourceID:  sourceID,
					url:       p.URL,
					ref:       sourceengine.ProviderRef{SourceID: sourceID, URL: p.URL, AddressMode: mode, WebURL: p.WebURL},
					sourceKey: sourceKey(p),
				}
				byKey[k] = grp
				order = append(order, k)
			}
			grp.providers = append(grp.providers, refreshProvider{
				title: sr.Title, provider: p.Provider, providerID: p.ID, scanlator: p.Scanlator,
			})
		}
	}

	groups := make([]refreshGroup, 0, len(order))
	for _, k := range order {
		grp := byKey[k]
		if !s.gateAvailable(ctx, grp.sourceKey, now) {
			slog.WarnContext(ctx, "refresh: skipping group — source cooled down by politeness gate",
				"source", grp.sourceID, "url", grp.url, "source_key", grp.sourceKey)
			continue
		}
		groups = append(groups, *grp)
	}
	return groups
}

// fetchableSourceID answers the per-provider question buildRefreshGroups asks of
// every provider of every monitored series: is there a live source behind this
// row that the sweep should re-fetch, and if so which one? It returns the numeric
// engine source id and ok=true only when all three conditions hold, logging the
// reason otherwise:
//
//   - The provider resolves to a numeric source id. The linked/disk-origin
//     decision is ONE rule shared with series + library
//     (series.LinkedProviderSourceID). Refresh used to carry its own copy, which
//     had already diverged (it did not trim surrounding whitespace), so a provider
//     stored as " 8 " counted as disk-origin here and as a live source everywhere
//     else. That is precisely the predicate deciding which providers the sweep
//     skips, so the copies must not fork.
//   - The manga URL is known — without it there is nothing to fetch.
//   - The owner has not PAUSED the source (QCAT-513). A paused source is not
//     re-polled at all. Nothing is deleted or downgraded: the provider row, its
//     ProviderChapter feed and every CBZ it produced stay exactly as they are, and
//     the source resumes on the next sweep after the owner un-pauses it. A series
//     whose providers are ALL paused therefore contributes no group at all, which
//     is a no-op sweep for it rather than an error.
//
// It is a separate function because folding three independent skip rules into the
// grouping loop pushed that loop past the repo's cognitive-complexity gate — the
// gate asking for exactly this split.
func (s *Service) fetchableSourceID(ctx context.Context, sr *ent.Series, p *ent.SeriesProvider, disabled map[int64]bool) (int64, bool) {
	sourceID, ok := series.LinkedProviderSourceID(p.Provider)
	if !ok {
		slog.WarnContext(ctx, "refresh: skipping provider with non-numeric provider id (disk-origin)",
			"series", sr.Title, "provider", p.Provider)
		return 0, false
	}
	if p.URL == "" {
		slog.WarnContext(ctx, "refresh: skipping provider with unknown url",
			"series", sr.Title, "provider", p.Provider)
		return 0, false
	}
	if disabled[sourceID] {
		slog.InfoContext(ctx, "refresh: skipping provider — source paused by owner",
			"series", sr.Title, "source", sourceID)
		return 0, false
	}
	return sourceID, true
}

// refreshGroup fetches one source-manga's chapter list ONCE (politeness delay +
// single UNCACHED, refresh-gated pre-fetch) and, on success, ingests every
// scanlator-provider that shares it from that single raw list. A fetch failure
// is recorded against the breaker once and marks every provider in the group as
// errored; a context cancellation is silently skipped (clean shutdown).
func (s *Service) refreshGroup(ctx context.Context, grp refreshGroup, now time.Time, mu *sync.Mutex, result *RefreshResult, sink *refreshEventSink) {
	// Politeness delay before the fetch — the runtime-tunable minimum gap between
	// successive requests to this physical source. This IS the gated call for the
	// group; AddSeriesWithChapters below is deliberately ungated (no double-Wait).
	s.gateWait(ctx, grp.sourceKey)
	// FRESH fetch (bypasses the shared interactive chapter cache): the sweep's own
	// (source, manga) grouping already dedups to one fetch per sweep, so refresh
	// gets its dedup from grouping, not the cache, and always sees new chapters —
	// the long, hot-reloadable interactive cache TTL can never stale-out discovery.
	// Every provider in a group shares one physical (source, manga url), so the
	// first provider's title feeds the engine host's chapter-number recognition
	// for the whole group's fetch (groups are never built with zero providers).
	start := time.Now()
	raw, fetchErr := s.ingest.FetchChaptersUncachedRef(ctx, grp.ref, grp.providers[0].title)
	fetchDuration := time.Since(start)
	if fetchErr != nil {
		s.handleGroupFetchError(ctx, grp, fetchErr, now, mu, result, sink, fetchDuration)
		return
	}
	s.gateRecordSuccess(ctx, grp.sourceKey)
	itemsCount := len(raw.Chapters)
	sink.add(newRefreshEvent(grp, sourceevents.StatusSuccess, fetchDuration, &itemsCount, nil))
	for _, p := range grp.providers {
		s.ingestProvider(ctx, grp, p, raw, mu, result)
	}
}

// handleGroupFetchError records a single-source-manga fetch failure: a context
// cancellation is skipped entirely (not a provider error, no breaker trip), else
// it trips the breaker once and marks every provider in the group errored +
// persists each one's sync-state failure.
func (s *Service) handleGroupFetchError(ctx context.Context, grp refreshGroup, fetchErr error, now time.Time, mu *sync.Mutex, result *RefreshResult, sink *refreshEventSink, fetchDuration time.Duration) {
	if isContextErr(fetchErr) {
		return
	}
	slog.ErrorContext(ctx, "refresh: group fetch failed",
		"source", grp.sourceID, "url", grp.url, "err", fetchErr)
	s.gateRecordFailure(ctx, grp.sourceKey, fetchErr, now)
	sink.add(newRefreshEvent(grp, sourceevents.StatusFailed, fetchDuration, nil, fetchErr))
	for _, p := range grp.providers {
		if uerr := s.upsertSyncState(ctx, p.providerID, fetchErr); uerr != nil {
			slog.ErrorContext(ctx, "refresh: persist sync state failed",
				"series", p.title, "provider", p.provider, "err", uerr)
		}
		mu.Lock()
		result.Errors++
		mu.Unlock()
	}
}

// ingestProvider ingests ONE scanlator-provider from the group's shared raw
// chapter list via AddSeriesWithChapters (no upstream fetch, no gate) and records
// the outcome (sync-state + counters), preserving the per-provider partial-success
// contract and the context-cancel skip.
func (s *Service) ingestProvider(ctx context.Context, grp refreshGroup, p refreshProvider, raw sourceengine.ChaptersResult, mu *sync.Mutex, result *RefreshResult) {
	res, addErr := s.ingest.AddSeriesWithChaptersRef(ctx, grp.ref, p.title, p.scanlator, raw)

	// Persist polling health; upsertSyncState skips on ctx-cancel.
	if uerr := s.upsertSyncState(ctx, p.providerID, addErr); uerr != nil {
		slog.ErrorContext(ctx, "refresh: persist sync state failed",
			"series", p.title, "provider", p.provider, "err", uerr)
	}

	mu.Lock()
	defer mu.Unlock()
	if addErr != nil {
		// Context cancellation (shutdown/timeout) is not a provider error — skip
		// counting/logging to avoid false error inflation on clean exit.
		if isContextErr(addErr) {
			return
		}
		slog.ErrorContext(ctx, "refresh: provider ingest failed",
			"series", p.title, "provider", p.provider, "err", addErr)
		result.Errors++
		return
	}
	result.ProvidersRefreshed++
	result.NewChapters += res.NewChapters
}

// isContextErr reports whether err is a context cancellation or deadline —
// treated everywhere in the sweep as clean shutdown, never a provider failure.
func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// sourceKey returns the physical-source identity used to key the
// source-politeness gate for a SeriesProvider: its display name
// (provider_name) when known, else its raw provider id, trimmed. It mirrors
// download.canonicalSourceKey — kept as a small local copy rather than a
// cross-package import so refresh does not need to know about the download
// engine's internals for this one shared concept.
func sourceKey(sp *ent.SeriesProvider) string {
	name := sp.ProviderName
	if name == "" {
		name = sp.Provider
	}
	return strings.TrimSpace(name)
}

// gateAvailable reports whether sourceKey's circuit-breaker currently permits
// access. A nil gate (no gate configured) is always available.
func (s *Service) gateAvailable(ctx context.Context, sourceKey string, now time.Time) bool {
	if s.gate == nil {
		return true
	}
	return s.gate.IsAvailable(ctx, sourceKey, now)
}

// gateWait enforces the politeness delay for sourceKey before a fetch. A nil
// gate is a no-op.
func (s *Service) gateWait(ctx context.Context, sourceKey string) {
	if s.gate == nil {
		return
	}
	s.gate.Wait(ctx, sourceKey)
}

// gateRecordSuccess reports a successful re-fetch from sourceKey to the
// breaker. A nil gate is a no-op.
func (s *Service) gateRecordSuccess(ctx context.Context, sourceKey string) {
	if s.gate == nil {
		return
	}
	s.gate.RecordSuccess(ctx, sourceKey)
}

// gateRecordFailure reports a failed re-fetch from sourceKey to the breaker. A
// nil gate is a no-op.
func (s *Service) gateRecordFailure(ctx context.Context, sourceKey string, cause error, now time.Time) {
	if s.gate == nil {
		return
	}
	s.gate.RecordFailure(ctx, sourceKey, cause, now)
}

// refreshEventSink accumulates the sweep's per-group audit events under its own
// lock, so the concurrent group goroutines can append without racing, and the
// whole batch is flushed in ONE LogBatch after the sweep. A nil sink (no recorder
// wired) makes add a no-op, so the collection cost is skipped entirely.
type refreshEventSink struct {
	mu     sync.Mutex
	events []sourceevents.Event
}

// add appends one event under the sink's lock. A nil sink is a no-op.
func (e *refreshEventSink) add(ev sourceevents.Event) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.events = append(e.events, ev)
	e.mu.Unlock()
}

// newEventSink returns a fresh sink when an audit recorder is wired, else nil (so
// the sweep collects nothing).
func (s *Service) newEventSink() *refreshEventSink {
	if s.events == nil {
		return nil
	}
	return &refreshEventSink{}
}

// flushEventSink logs the sweep's collected events in one batch (best-effort,
// nil-guarded). Called once after the sweep's goroutines have all joined, so the
// slice is safe to read without further locking.
func (s *Service) flushEventSink(ctx context.Context, sink *refreshEventSink) {
	if s.events == nil || sink == nil || len(sink.events) == 0 {
		return
	}
	s.events.LogBatch(ctx, sink.events)
}

// newRefreshEvent builds a `refresh` audit event for one source-manga group.
func newRefreshEvent(grp refreshGroup, status sourceevents.Status, duration time.Duration, itemsCount *int, cause error) sourceevents.Event {
	return sourceevents.Event{
		SourceKey:  grp.sourceKey,
		SourceID:   strconv.FormatInt(grp.sourceID, 10),
		SourceName: grp.sourceKey,
		Type:       sourceevents.EventRefresh,
		Status:     status,
		Duration:   duration,
		Err:        cause,
		ItemsCount: itemsCount,
		Metadata:   map[string]string{"url": grp.url},
	}
}

// upsertSyncState records the outcome of refreshing one provider into its
// SuwayomiSyncState row, creating the row the first time. A nil syncErr means
// success (stamp last_synced_at, clear last_error); a non-nil syncErr records
// last_error. Context cancellation / deadline exceeded is silently skipped
// (clean shutdown, not a bookkeeping event). It never deletes anything.
func (s *Service) upsertSyncState(ctx context.Context, providerID uuid.UUID, syncErr error) error {
	// Skip on clean cancellation — this is shutdown, not a real fetch failure.
	if errors.Is(syncErr, context.Canceled) || errors.Is(syncErr, context.DeadlineExceeded) {
		return nil
	}
	now := time.Now().UTC()
	existing, err := s.client.SuwayomiSyncState.Query().
		Where(entsuwayomisyncstate.SeriesProviderID(providerID)).
		Only(ctx)
	if ent.IsNotFound(err) {
		c := s.client.SuwayomiSyncState.Create().SetSeriesProviderID(providerID)
		if syncErr == nil {
			c = c.SetLastSyncedAt(now)
		} else {
			c = c.SetLastError(syncErr.Error())
		}
		return c.Exec(ctx)
	}
	if err != nil {
		return fmt.Errorf("refresh.upsertSyncState: query %s: %w", providerID, err)
	}
	u := s.client.SuwayomiSyncState.UpdateOneID(existing.ID)
	if syncErr == nil {
		u = u.SetLastSyncedAt(now).SetLastError("")
	} else {
		u = u.SetLastError(syncErr.Error())
	}
	return u.Exec(ctx)
}
