package refresh

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

type fixedRefreshConcurrency int

func (f fixedRefreshConcurrency) RefreshConcurrency(context.Context) int { return int(f) }

// admissionProbeClient blocks every chapter fetch until cancellation and reports
// each admitted source first. Embedding the production interface keeps the test
// double focused on the only engine operation a refresh group performs.
type admissionProbeClient struct {
	sourceengine.Client
	started chan int64
}

func (c *admissionProbeClient) Chapters(ctx context.Context, sourceID int64, _ string, _ string) ([]sourceengine.Chapter, error) {
	c.started <- sourceID
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestSweep_AdmissionIsFairAcrossSources catches a return to discovery-order
// admission: if the first source fills the global limit, the second source must
// still receive one of those slots while the first has groups waiting.
//
// The hand-built series list fixes discovery order and every admitted fetch
// blocks, so the first two observations are the complete global admission set;
// this proof does not depend on database ordering or goroutine timing.
func TestSweep_AdmissionIsFairAcrossSources(t *testing.T) {
	const (
		busySource    = int64(11)
		healthySource = int64(22)
		globalLimit   = 2
	)

	client := &admissionProbeClient{started: make(chan int64, globalLimit)}
	svc := NewService(nil, ingest.NewIngest(client, nil), sse.NewHub(), fixedRefreshConcurrency(globalLimit), nil)
	seriesList := []*ent.Series{
		refreshTestSeries("busy-1", busySource, "/busy/1"),
		refreshTestSeries("busy-2", busySource, "/busy/2"),
		refreshTestSeries("busy-3", busySource, "/busy/3"),
		refreshTestSeries("healthy", healthySource, "/healthy"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan RefreshResult, 1)
	go func() { done <- svc.sweep(ctx, seriesList) }()

	admitted := []int64{
		awaitRefreshAdmission(t, client.started),
		awaitRefreshAdmission(t, client.started),
	}
	cancel()
	var result RefreshResult
	select {
	case result = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled sweep did not stop")
	}

	healthyAdmissions := 0
	for _, sourceID := range admitted {
		if sourceID == healthySource {
			healthyAdmissions++
		}
	}
	if healthyAdmissions != 1 {
		t.Fatalf("first %d admissions = %v, want one source %d group while source %d still has queued work", globalLimit, admitted, healthySource, busySource)
	}
	if result.Errors != 0 {
		t.Fatalf("canceled sweep errors = %d, want 0", result.Errors)
	}
}

type refreshContainmentClient struct {
	sourceengine.Client
	busySource       int64
	healthySource    int64
	busyFirstStarted chan struct{}
	busyStarted      chan struct{}
	busyRelease      <-chan struct{}
	healthyStarted   chan struct{}
	busyOnce         sync.Once
	healthyOnce      sync.Once
	current          atomic.Int32
	peak             atomic.Int32
}

func (c *refreshContainmentClient) Chapters(ctx context.Context, sourceID int64, _ string, _ string) ([]sourceengine.Chapter, error) {
	leave := c.enter()
	defer leave()

	switch sourceID {
	case c.busySource:
		return c.fetchBusy(ctx)
	case c.healthySource:
		return c.fetchHealthy(ctx)
	default:
		return nil, errors.New("unexpected source")
	}
}

func (c *refreshContainmentClient) enter() func() {
	current := c.current.Add(1)
	for {
		peak := c.peak.Load()
		if current <= peak || c.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	return func() { c.current.Add(-1) }
}

func (c *refreshContainmentClient) fetchBusy(ctx context.Context) ([]sourceengine.Chapter, error) {
	c.busyOnce.Do(func() { close(c.busyFirstStarted) })
	c.busyStarted <- struct{}{}
	select {
	case <-c.busyRelease:
		return nil, errors.New("cloudflare challenge detected")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *refreshContainmentClient) fetchHealthy(ctx context.Context) ([]sourceengine.Chapter, error) {
	c.healthyOnce.Do(func() { close(c.healthyStarted) })
	select {
	case <-c.busyFirstStarted:
		return []sourceengine.Chapter{{Number: 1, URL: "healthy-1"}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestSweep_ChallengedBacklogCannotConsumeHealthyProgress drives the real
// refresh and ingest paths with three challenged groups discovered before one
// healthy group. The healthy feed must persist before the challenged calls are
// released, while the existing sweep-wide limit still bounds actual engine use.
func TestSweep_ChallengedBacklogCannotConsumeHealthyProgress(t *testing.T) {
	const (
		busySource    = int64(11)
		healthySource = int64(22)
		globalLimit   = 2
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db := testdb.New(t)
	seriesList := []*ent.Series{
		seedRefreshContainmentSeries(t, ctx, db, "refresh-busy-1", busySource, "/busy/1", "refresh-challenged"),
		seedRefreshContainmentSeries(t, ctx, db, "refresh-busy-2", busySource, "/busy/2", "refresh-challenged"),
		seedRefreshContainmentSeries(t, ctx, db, "refresh-busy-3", busySource, "/busy/3", "refresh-challenged"),
	}
	healthy := seedRefreshContainmentSeries(t, ctx, db, "refresh-healthy", healthySource, "/healthy", "refresh-healthy")
	seriesList = append(seriesList, healthy)
	healthyProvider := healthy.Edges.Providers[0]

	busyRelease := make(chan struct{})
	var releaseOnce sync.Once
	releaseBusy := func() { releaseOnce.Do(func() { close(busyRelease) }) }
	t.Cleanup(releaseBusy)
	engine := &refreshContainmentClient{
		Client: enginefake.New(enginefake.WithSources([]sourceengine.Source{
			{ID: busySource, Name: "refresh-challenged", Lang: "en"},
			{ID: healthySource, Name: "refresh-healthy", Lang: "en"},
		})),
		busySource:       busySource,
		healthySource:    healthySource,
		busyFirstStarted: make(chan struct{}),
		busyStarted:      make(chan struct{}, 3),
		busyRelease:      busyRelease,
		healthyStarted:   make(chan struct{}),
	}
	healthyIngested := observeRefreshContainmentFeed(db, healthyProvider.ID)
	policy := settings.Static{
		Concurrency:          globalLimit,
		SourcesFailureThresh: 2,
		SourcesCooldownIv:    time.Hour,
	}
	gate := sourcegate.NewService(db, policy)
	svc := NewService(db, ingest.NewIngest(engine, db), sse.NewHub(), policy, gate)

	done := make(chan RefreshResult, 1)
	go func() { done <- svc.sweep(ctx, seriesList) }()
	awaitRefreshContainment(t, engine.busyFirstStarted, "first challenged refresh")
	awaitRefreshContainment(t, engine.healthyStarted, "healthy refresh admission")
	awaitRefreshContainment(t, healthyIngested, "healthy feed persistence")
	for range 2 {
		awaitRefreshContainment(t, engine.busyStarted, "challenged refresh admission")
	}

	if got := db.ProviderChapter.Query().Where(entproviderchapter.SeriesProviderIDEQ(healthyProvider.ID)).CountX(ctx); got != 1 {
		t.Fatalf("healthy provider chapters = %d, want 1 before challenged backlog is released", got)
	}
	if got := engine.peak.Load(); got != globalLimit {
		t.Fatalf("peak refresh occupancy = %d, want exact global limit %d", got, globalLimit)
	}

	releaseBusy()
	result := awaitRefreshContainmentResult(t, done)
	if result.SeriesRefreshed != 4 || result.ProvidersRefreshed != 1 || result.NewChapters != 1 || result.Errors != 3 {
		t.Fatalf("refresh result = %+v, want series=4 providers=1 new=1 errors=3", result)
	}
	if got := engine.peak.Load(); got != globalLimit {
		t.Fatalf("final peak refresh occupancy = %d, want %d", got, globalLimit)
	}
}

func seedRefreshContainmentSeries(t *testing.T, ctx context.Context, db *ent.Client, title string, sourceID int64, url, providerName string) *ent.Series {
	t.Helper()
	series := db.Series.Create().SetTitle(title).SetSlug(disk.Slugify(title)).SetMonitored(true).SaveX(ctx)
	provider := db.SeriesProvider.Create().
		SetSeries(series).
		SetProvider(strconv.FormatInt(sourceID, 10)).
		SetProviderName(providerName).
		SetURL(url).
		SetImportance(10).
		SaveX(ctx)
	series.Edges.Providers = []*ent.SeriesProvider{provider}
	return series
}

func observeRefreshContainmentFeed(client *ent.Client, providerID uuid.UUID) <-chan struct{} {
	created := make(chan struct{})
	var once sync.Once
	client.ProviderChapter.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, mutation)
			providerChapter, ok := mutation.(*ent.ProviderChapterMutation)
			if err == nil && ok {
				if id, exists := providerChapter.SeriesProviderID(); exists && id == providerID {
					once.Do(func() { close(created) })
				}
			}
			return value, err
		})
	})
	return created
}

func awaitRefreshContainment(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func awaitRefreshContainmentResult(t *testing.T, done <-chan RefreshResult) RefreshResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for challenged refresh sweep")
		return RefreshResult{}
	}
}

func awaitRefreshAdmission(t *testing.T, started <-chan int64) int64 {
	t.Helper()
	select {
	case sourceID := <-started:
		return sourceID
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for refresh admission")
		return 0
	}
}

// TestRoundRobinRefreshGroups_PreservesEveryGroupAndSourceOrder pins the queue's
// lossless property alongside fairness: reordering admission must neither drop
// nor duplicate a group, and work discovered earlier within one source remains
// earlier for that source.
func TestRoundRobinRefreshGroups_PreservesEveryGroupAndSourceOrder(t *testing.T) {
	groups := []refreshGroup{
		{sourceID: 11, url: "/a/1"},
		{sourceID: 11, url: "/a/2"},
		{sourceID: 22, url: "/b/1"},
		{sourceID: 11, url: "/a/3"},
		{sourceID: 33, url: "/c/1"},
		{sourceID: 22, url: "/b/2"},
	}

	ordered := roundRobinRefreshGroups(groups)
	got := make([]string, 0, len(ordered))
	for _, grp := range ordered {
		got = append(got, grp.url)
	}
	want := []string{"/a/1", "/b/1", "/c/1", "/a/2", "/b/2", "/a/3"}
	if len(got) != len(want) {
		t.Fatalf("ordered groups = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ordered groups = %v, want %v", got, want)
		}
	}
}

func refreshTestSeries(title string, sourceID int64, url string) *ent.Series {
	return &ent.Series{
		Title: title,
		Edges: ent.SeriesEdges{Providers: []*ent.SeriesProvider{{
			ID:       uuid.New(),
			Provider: strconv.FormatInt(sourceID, 10),
			URL:      url,
		}}},
	}
}
