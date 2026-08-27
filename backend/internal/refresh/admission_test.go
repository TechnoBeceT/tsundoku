package refresh

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/sourceengine"
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
