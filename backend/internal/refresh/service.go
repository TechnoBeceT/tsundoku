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
}

// WithEventRecorder attaches the source-operation audit-log recorder so each
// sweep logs a `refresh` event per source-manga group. It returns the receiver
// for chaining off NewService. A nil recorder logs nothing (best-effort — never
// affects the sweep).
func (s *Service) WithEventRecorder(r sourceevents.Recorder) *Service {
	s.events = r
	return s
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

	s.broadcast("refresh.start", RefreshEvent{Monitored: len(seriesList)})

	now := time.Now()
	// Group every provider by its (physical source, manga) so the sweep fetches
	// each source-manga's chapter list ONCE and ingests every scanlator-provider
	// that shares it from that single result (Task A — de-amplification). A series
	// followed under three scanlators of the same source used to trigger three
	// identical FetchChapters; now it triggers one.
	groups := s.buildRefreshGroups(ctx, seriesList, now)

	var mu sync.Mutex
	result := RefreshResult{SeriesRefreshed: len(seriesList)}
	// sink collects one `refresh` audit event per group, flushed ONCE after the
	// sweep (nil when no recorder is wired, so nothing is collected).
	sink := s.newEventSink()

	g, gctx := errgroup.WithContext(ctx)
	// Read the parallel-refetch bound at use-time so a settings change applies to
	// this sweep (clamped >= 1 — a 0 limit would deadlock errgroup). The bound now
	// caps concurrent GROUPS (each = one upstream fetch) rather than providers.
	g.SetLimit(s.refreshLimit(ctx))
	for _, grp := range groups {
		g.Go(func() error {
			s.refreshGroup(gctx, grp, now, &mu, &result, sink)
			return nil
		})
	}
	// Goroutines never return non-nil, so Wait never errors; parent-ctx
	// cancellation surfaces as context.Canceled in the fetch/ingest and is skipped.
	_ = g.Wait()
	s.flushEventSink(ctx, sink)

	s.broadcast("refresh.done", RefreshEvent{
		Monitored:          len(seriesList),
		SeriesRefreshed:    result.SeriesRefreshed,
		ProvidersRefreshed: result.ProvidersRefreshed,
		NewChapters:        result.NewChapters,
		Errors:             result.Errors,
	})
	return result, nil
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
	sourceKey string
	providers []refreshProvider
}

// buildRefreshGroups flattens every monitored series' providers into groups keyed
// by (numeric source id, manga url), skipping a provider whose Provider column
// does not parse as a numeric source id (a disk-origin row — never had a live
// source attached, so there is nothing to fetch) or whose URL is unknown. A
// whole group whose physical source is currently cooled down by the
// source-politeness gate is dropped (a tripped source is excluded from the
// sweep entirely this cycle, mirroring the download dispatcher's candidacy
// exclusion). Extracted from RefreshAll to keep its cyclomatic complexity low.
func (s *Service) buildRefreshGroups(ctx context.Context, seriesList []*ent.Series, now time.Time) []refreshGroup {
	type key struct {
		source int64
		url    string
	}
	byKey := make(map[key]*refreshGroup)
	var order []key
	for _, sr := range seriesList {
		for _, p := range sr.Edges.Providers {
			sourceID, ok := parseProviderSourceID(p.Provider)
			if !ok {
				slog.WarnContext(ctx, "refresh: skipping provider with non-numeric provider id (disk-origin)",
					"series", sr.Title, "provider", p.Provider)
				continue
			}
			if p.URL == "" {
				slog.WarnContext(ctx, "refresh: skipping provider with unknown url",
					"series", sr.Title, "provider", p.Provider)
				continue
			}
			k := key{source: sourceID, url: p.URL}
			grp, ok := byKey[k]
			if !ok {
				grp = &refreshGroup{sourceID: sourceID, url: p.URL, sourceKey: sourceKey(p)}
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

// parseProviderSourceID parses a SeriesProvider.Provider column into the
// numeric engine-host source id ingest.Ingest expects. A disk-origin provider
// stores a display name (e.g. "Other" or a slug), which never parses — ok is
// false in that case, and the caller skips the provider (there is no live
// source to re-fetch from).
func parseProviderSourceID(provider string) (id int64, ok bool) {
	n, err := strconv.ParseInt(provider, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
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
	raw, fetchErr := s.ingest.FetchChaptersUncached(ctx, grp.sourceID, grp.url, grp.providers[0].title)
	fetchDuration := time.Since(start)
	if fetchErr != nil {
		s.handleGroupFetchError(ctx, grp, fetchErr, now, mu, result, sink, fetchDuration)
		return
	}
	s.gateRecordSuccess(ctx, grp.sourceKey)
	itemsCount := len(raw)
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
func (s *Service) ingestProvider(ctx context.Context, grp refreshGroup, p refreshProvider, raw []sourceengine.Chapter, mu *sync.Mutex, result *RefreshResult) {
	res, addErr := s.ingest.AddSeriesWithChapters(ctx, grp.sourceID, grp.url, p.title, p.scanlator, raw)

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
