// Package download implements the M1 state-driven download dispatcher.
//
// The dispatcher loads all actionable chapters (state=wanted, or state=failed
// with retry budget remaining), fetches their pages via the ChapterFetcher
// port, renders them to disk via the disk.RenderChapter renderer, and advances
// chapter state through the state machine. Per-provider concurrency is capped
// via buffered-channel semaphores so that a single provider cannot monopolise
// the worker pool.
package download

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/sse"
)

// Config holds the tunable parameters for the Dispatcher.
type Config struct {
	// PerProviderConcurrency is the maximum number of chapters from the same
	// provider that may be downloaded concurrently. Must be >= 1.
	PerProviderConcurrency int

	// MaxRetries is the maximum number of times a failed chapter is retried
	// before it is transitioned to permanently_failed.
	MaxRetries int

	// Backoff returns the duration to wait before the next download attempt for
	// a chapter that has already been tried attempt times. Nil uses a simple
	// default exponential backoff.
	Backoff func(attempt int) time.Duration

	// Storage is the root library directory (e.g. "/data/library") passed to
	// disk.RenderChapter.
	Storage string
}

// defaultBackoff is the backoff function used when Config.Backoff is nil.
// It doubles the delay for each attempt up to a maximum of 1 hour.
//
// Overflow analysis: base = 5 min = 3e11 ns. The hour cap fires at shift=4
// (5min×2^4=80min>1h), so shift is capped at 12 as a conservative guard.
// At shift=12: 5min×2^12 ≈ 1.2e15 ns — far below int64 max (≈9.2e18 ns).
func defaultBackoff(attempt int) time.Duration {
	base := 5 * time.Minute
	shift := attempt
	// Cap shift at 12 so the multiplication stays well within int64 range.
	// The hour ceiling already kicks in at shift=4; the cap prevents any
	// theoretical overflow on very large attempt counts.
	if shift > 12 {
		shift = 12
	}
	d := base * (1 << uint(shift)) //nolint:gosec // shift is capped at 12; 5min×2^12 ≈ 1.2e15ns << int64 max.
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

// DownloadEvent is the SSE payload broadcast for every download lifecycle
// transition (start, done, fail). ChapterID identifies the affected chapter;
// State is the new chapter state; Error is set only on failure.
type DownloadEvent struct {
	// ChapterID is the UUID of the chapter that changed state.
	ChapterID uuid.UUID `json:"chapter_id"`

	// State is the new state of the chapter after the transition.
	State string `json:"state"`

	// Error is the human-readable error message. Set only on failure events.
	Error string `json:"error,omitempty"`
}

// Dispatcher coordinates the M1 download pipeline. Create one with New and call
// RunOnce to process all currently actionable chapters.
type Dispatcher struct {
	client *ent.Client
	f      fetcher.ChapterFetcher
	hub    *sse.Hub
	cfg    Config
}

// New creates a Dispatcher configured with the given client, fetcher, SSE hub,
// and Config. If cfg.Backoff is nil the default exponential backoff is used.
// cfg.PerProviderConcurrency and cfg.MaxRetries must be >= 1.
func New(client *ent.Client, f fetcher.ChapterFetcher, hub *sse.Hub, cfg Config) *Dispatcher {
	if cfg.PerProviderConcurrency < 1 {
		cfg.PerProviderConcurrency = 1
	}
	if cfg.MaxRetries < 1 {
		cfg.MaxRetries = 1
	}
	if cfg.Backoff == nil {
		cfg.Backoff = defaultBackoff
	}
	return &Dispatcher{
		client: client,
		f:      f,
		hub:    hub,
		cfg:    cfg,
	}
}

// RunOnce loads all currently actionable chapters and processes them
// concurrently, honouring the per-provider concurrency cap. It waits for all
// downloads to complete before returning. Per-chapter errors are collected; the
// first non-nil error is returned. Callers can run this method in a ticker loop.
//
// BestProviderChapter is resolved exactly once per chapter here so that the
// semaphore key and the actual fetch target are always the same provider
// snapshot, preventing a TOCTOU skew if provider importance changes mid-run.
func (d *Dispatcher) RunOnce(ctx context.Context) error {
	chapters, err := chapter.WantedChapters(ctx, d.client, 1000, d.cfg.MaxRetries)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.RunOnce: load chapters: %w", err)
	}
	if len(chapters) == 0 {
		return nil
	}

	// Per-provider semaphores: one buffered channel per provider name.
	type semaphore = chan struct{}
	var mu sync.Mutex
	sems := make(map[string]semaphore)

	getSem := func(provider string) semaphore {
		mu.Lock()
		defer mu.Unlock()
		if _, ok := sems[provider]; !ok {
			sems[provider] = make(semaphore, d.cfg.PerProviderConcurrency)
		}
		return sems[provider]
	}

	var wg sync.WaitGroup

	for _, ch := range chapters {
		chID := ch.ID

		// Resolve the best provider ONCE per chapter so the semaphore key and the
		// fetch target are guaranteed to be the same provider.
		pc, importance, err := chapter.BestProviderChapter(ctx, d.client, chID)
		if err != nil {
			// No-provider is near-defensive: the ingest invariant guarantees every
			// Chapter is created alongside at least one ProviderChapter. If that
			// invariant is violated (e.g. a manual DB edit), the chapter should stay
			// in wanted — it is awaiting a source via ingest, NOT a fetch failure.
			// Transitioning to failed here would be an illegal state-graph edge.
			// Emit an observable skip notice and continue; do not silently swallow.
			slog.WarnContext(ctx, "download.RunOnce: no provider for chapter — skipping this round; chapter stays wanted until ingest supplies a provider",
				"chapter_id", chID,
				"err", err,
			)
			d.broadcast("download.skip", DownloadEvent{
				ChapterID: chID,
				State:     string(entchapter.StateWanted),
				Error:     "no provider available: " + err.Error(),
			})
			continue
		}
		sp := pc.Edges.SeriesProvider
		providerName := ""
		if sp != nil {
			providerName = sp.Provider
		}

		sem := getSem(providerName)
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// Per-chapter errors are recorded in the DB and broadcast via SSE.
			// They are not propagated to the caller — a failed chapter is a
			// handled outcome, not an infrastructure failure.
			_ = d.process(ctx, chID, pc, importance)
		}()
	}

	wg.Wait()
	return nil
}

// Process executes the full download pipeline for a single chapter by chapter
// ID. It resolves the best provider internally and delegates to the internal
// process method. Callers that have already resolved the best ProviderChapter
// (e.g. RunOnce) should use process directly to avoid a redundant DB query.
func (d *Dispatcher) Process(ctx context.Context, chapterID uuid.UUID) error {
	pc, importance, err := chapter.BestProviderChapter(ctx, d.client, chapterID)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.Process: best provider for chapter %s: %w", chapterID, err)
	}
	return d.process(ctx, chapterID, pc, importance)
}

// process executes the full download pipeline for a single chapter using a
// pre-resolved ProviderChapter. RunOnce calls this directly so that the
// BestProviderChapter resolution is never duplicated (semaphore key and fetch
// target are always the same provider snapshot).
//
//  1. Load the chapter with its series edge.
//  2. Transition the chapter to downloading.
//  3. Broadcast a download.start SSE event.
//  4. Build a FetchRef and fetch pages.
//  5. On success: render to disk, update provenance fields, transition to
//     downloaded, broadcast download.done.
//  6. On failure: call handleFailure, which increments retries, sets last_error
//     and next_attempt_at, transitions to permanently_failed when the retry
//     budget is exhausted, and broadcasts download.fail.
func (d *Dispatcher) process(ctx context.Context, chapterID uuid.UUID, pc *ent.ProviderChapter, importance int) error {
	ch, err := d.client.Chapter.Query().
		Where(entchapter.IDEQ(chapterID)).
		WithSeries().
		Only(ctx)
	if err != nil {
		return fmt.Errorf("download.Dispatcher.process: load chapter %s: %w", chapterID, err)
	}

	sp := pc.Edges.SeriesProvider

	// Transition wanted / failed → downloading.
	if err := chapter.SetState(ctx, d.client, chapterID, entchapter.StateDownloading); err != nil {
		return fmt.Errorf("download.Dispatcher.process: transition to downloading for chapter %s: %w", chapterID, err)
	}

	d.broadcast("download.start", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateDownloading),
	})

	ref := buildFetchRef(pc, sp)

	pages, fetchErr := d.f.Fetch(ctx, ref)
	if fetchErr != nil {
		return d.handleFailure(ctx, ch, chapterID, fetchErr)
	}

	// Render to disk.
	maxChap := maxChapterNumber(ctx, d.client, ch.SeriesID)
	meta := buildRenderMeta(ch, pc, sp, maxChap)
	filename, renderErr := disk.RenderChapter(disk.RenderRequest{
		Storage: d.cfg.Storage,
		Meta:    meta,
		Pages:   pages.Pages,
	})
	if renderErr != nil {
		return d.handleFailure(ctx, ch, chapterID, renderErr)
	}

	// Persist provenance on the Chapter row.
	now := time.Now()
	pageCount := pages.PageCount
	if err := d.client.Chapter.UpdateOneID(chapterID).
		SetSatisfiedByProviderID(sp.ID).
		SetSatisfiedImportance(importance).
		SetFilename(filename).
		SetPageCount(pageCount).
		SetDownloadDate(now).
		SetLastError("").
		Exec(ctx); err != nil {
		// Route through handleFailure so the chapter transitions out of
		// downloading (→ failed / permanently_failed) rather than stranding there.
		// Re-downloading on retry is safe: RenderChapter upserts the CBZ.
		return d.handleFailure(ctx, ch, chapterID, fmt.Errorf("persist provenance: %w", err))
	}

	if err := chapter.SetState(ctx, d.client, chapterID, entchapter.StateDownloaded); err != nil {
		// Defensive path: only reachable on DB failure between the provenance
		// update and this state transition. Route through handleFailure so the
		// chapter is never left in downloading.
		return d.handleFailure(ctx, ch, chapterID, fmt.Errorf("transition to downloaded: %w", err))
	}

	d.broadcast("download.done", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateDownloaded),
	})

	return nil
}

// handleFailure records a failed download attempt and transitions the chapter
// to the appropriate terminal (permanently_failed) or retry (failed) state.
//
// ch is the snapshot of the Chapter row taken at Process entry. The
// wanted/failed→downloading state-gate in SetState prevents double-processing,
// so ch.Retries reflects the value at Process entry and is not stale in practice.
//
// It increments the retry counter, stores the cause as last_error, computes
// next_attempt_at via cfg.Backoff, then:
//   - If newRetries >= MaxRetries: transitions to permanently_failed.
//   - Otherwise: leaves the chapter in failed state (SetState downloading→failed).
//
// It always broadcasts a download.fail event and returns the original cause.
func (d *Dispatcher) handleFailure(ctx context.Context, ch *ent.Chapter, chapterID uuid.UUID, cause error) error {
	newRetries := ch.Retries + 1
	nextAttempt := time.Now().Add(d.cfg.Backoff(newRetries))

	// Transition downloading → failed.
	if setErr := chapter.SetState(ctx, d.client, chapterID, entchapter.StateFailed); setErr != nil {
		// Defensive path: only reachable if the DB connection is lost between the
		// downloading transition and this failure handler — not reachable under
		// normal operation.
		return fmt.Errorf("download.Dispatcher.handleFailure: transition to failed for chapter %s: %w", chapterID, setErr)
	}

	if err := d.client.Chapter.UpdateOneID(chapterID).
		SetRetries(newRetries).
		SetLastError(cause.Error()).
		SetNextAttemptAt(nextAttempt).
		Exec(ctx); err != nil {
		return fmt.Errorf("download.Dispatcher.handleFailure: update retry fields for chapter %s: %w", chapterID, err)
	}

	if newRetries >= d.cfg.MaxRetries {
		if setErr := chapter.SetState(ctx, d.client, chapterID, entchapter.StatePermanentlyFailed); setErr != nil {
			// Defensive path: only reachable on DB failure between the failed
			// transition and this permanent-failure escalation.
			return fmt.Errorf("download.Dispatcher.handleFailure: transition to permanently_failed for chapter %s: %w", chapterID, setErr)
		}
		d.broadcast("download.fail", DownloadEvent{
			ChapterID: chapterID,
			State:     string(entchapter.StatePermanentlyFailed),
			Error:     cause.Error(),
		})
		return cause
	}

	d.broadcast("download.fail", DownloadEvent{
		ChapterID: chapterID,
		State:     string(entchapter.StateFailed),
		Error:     cause.Error(),
	})
	return cause
}

// broadcast emits an SSE event of the given type with data as the JSON payload.
// JSON encoding errors are silently discarded — a missing SSE event is
// preferable to crashing the dispatcher goroutine.
func (d *Dispatcher) broadcast(eventType string, data DownloadEvent) {
	// Pre-marshal to ensure the Data field is a concrete type that the SSE
	// handler can JSON-encode without reflection surprises.
	raw, err := json.Marshal(data)
	if err != nil {
		// Defensive path: DownloadEvent contains only uuid.UUID and string fields,
		// so Marshal should never fail. Document as unreachable.
		return
	}
	d.hub.Broadcast(sse.Event{
		Type: eventType,
		Data: json.RawMessage(raw),
	})
}

// buildRenderMeta constructs a disk.RenderMeta from the Chapter, its
// best ProviderChapter, the owning SeriesProvider, and the series' max chapter
// number (for zero-padding).
//
// Known limitation (matches legacy Kaizoku.GO): as the series max grows,
// previously-rendered files keep their old (narrower) padding until re-rendered.
// Acceptable for M1.
func buildRenderMeta(ch *ent.Chapter, pc *ent.ProviderChapter, sp *ent.SeriesProvider, maxChapter *float64) disk.RenderMeta {
	seriesTitle := ""
	// Default to Other when the series edge is unloaded/absent (same guard as the
	// title): a downloaded chapter must still render somewhere valid.
	category := disk.CategoryOther
	if ch.Edges.Series != nil {
		seriesTitle = ch.Edges.Series.Title
		category = disk.Category(ch.Edges.Series.Category.String())
	}
	return disk.RenderMeta{
		Provider:            sp.Provider,
		Scanlator:           sp.Scanlator,
		Language:            sp.Language,
		SeriesTitle:         seriesTitle,
		Category:            category,
		Number:              pc.Number,
		MaxChapter:          maxChapter,
		ChapterName:         pc.Name,
		ChapterKey:          pc.ChapterKey,
		UploadDate:          pc.ProviderUploadDate,
		URL:                 pc.URL,
		Importance:          sp.Importance,
		SeriesProviderTitle: sp.Title,
	}
}

// buildFetchRef constructs a fetcher.FetchRef from a ProviderChapter and its
// owning SeriesProvider. It is the single place that maps provider-row fields
// to the fetch port's input type, shared by process and Upgrade so that no
// ref-building logic is duplicated.
//
// SuwayomiID comes from the ProviderChapter row (the per-chapter Suwayomi ID),
// not from SeriesProvider.SuwayomiID (which is the manga/series-level ID used
// for MangaChapters queries). The chapter-level ID is required by
// suwayomi.Fetcher.Fetch → client.ChapterPages(ctx, ref.SuwayomiID).
func buildFetchRef(pc *ent.ProviderChapter, sp *ent.SeriesProvider) fetcher.FetchRef {
	return fetcher.FetchRef{
		Provider:         sp.Provider,
		Scanlator:        sp.Scanlator,
		Language:         sp.Language,
		URL:              pc.URL,
		SuwayomiID:       pc.SuwayomiChapterID,
		SeriesProviderID: sp.ID,
	}
}

// maxChapterNumber returns the highest chapter number across all ProviderChapters
// for the given series, used to zero-pad CBZ filenames to consistent width.
// Returns nil if no numbered chapters exist for this series.
func maxChapterNumber(ctx context.Context, client *ent.Client, seriesID uuid.UUID) *float64 {
	var result []struct {
		Max *float64 `json:"max"`
	}
	err := client.ProviderChapter.Query().
		Where(
			entproviderchapter.HasSeriesProviderWith(
				entseriesprovider.SeriesIDEQ(seriesID),
			),
			entproviderchapter.NumberNotNil(),
		).
		Aggregate(ent.Max(entproviderchapter.FieldNumber)).
		Scan(ctx, &result)
	if err != nil || len(result) == 0 || result[0].Max == nil {
		// Defensive path: on query failure or no numbered chapters, fall back to
		// unpadded filenames — non-critical for correctness.
		return nil
	}
	return result[0].Max
}
