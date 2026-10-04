package imports_test

import (
	"context"
	"errors"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"testing"
	"time"
)

type streamClient struct {
	*fakeClient
	blocked      chan struct{}
	cancelled    chan struct{}
	blockSources bool
	started      chan struct{}
}

func (c *streamClient) Sources(ctx context.Context) ([]sourceengine.Source, error) {
	if c.blockSources {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return c.fakeClient.Sources(ctx)
}
func (c *streamClient) Search(ctx context.Context, id int64, q string, p int) (sourceengine.SearchResult, error) {
	if id == 2 {
		if c.started != nil {
			close(c.started)
		}
		select {
		case <-c.blocked:
		case <-ctx.Done():
			if c.cancelled != nil {
				close(c.cancelled)
			}
			return sourceengine.SearchResult{}, ctx.Err()
		}
	}
	return c.fakeClient.Search(ctx, id, q, p)
}
func newStreamClient() *streamClient {
	return &streamClient{fakeClient: &fakeClient{sources: []sourceengine.Source{{ID: 1, Name: "Healthy"}, {ID: 2, Name: "Slow"}}, searchResults: map[int64]sourceengine.SearchResult{1: {Manga: []sourceengine.MangaEntry{{Title: "Alpha", URL: "/alpha"}}}, 2: {Manga: []sourceengine.MangaEntry{{Title: "Beta", URL: "/beta"}}}}}, blocked: make(chan struct{})}
}
func TestSearchStreamHealthyBeforeBlocked(t *testing.T) {
	c := newStreamClient()
	svc := imports.NewService(c, nil, nil, "", time.Second, nil)
	snapshots := make(chan imports.SearchSnapshotDTO, 8)
	finished := make(chan error, 1)
	go func() {
		finished <- svc.SearchStream(context.Background(), "alpha", nil, func(s imports.SearchSnapshotDTO) error { snapshots <- s; return nil })
	}()
	first := receiveSearch(t, snapshots)
	if first.Done || len(first.Groups) != 0 || len(first.PendingSources) != 2 {
		t.Fatalf("initial=%+v", first)
	}
	partial := receiveSearch(t, snapshots)
	if partial.Done || len(partial.Groups) != 1 || len(partial.PendingSources) != 1 || partial.PendingSources[0].ID != "2" {
		t.Fatalf("partial=%+v", partial)
	}
	close(c.blocked)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	assertCompletedSearch(t, snapshots, 2)
}
func TestSearchStreamDeadlineAndSourceFailure(t *testing.T) {
	c := newStreamClient()
	c.fakeClient.searchErrs = map[int64]error{1: errors.New("unavailable")}
	svc := imports.NewService(c, nil, nil, "", 30*time.Millisecond, nil)
	var final imports.SearchSnapshotDTO
	if err := svc.SearchStream(context.Background(), "alpha", nil, func(s imports.SearchSnapshotDTO) error { final = s; return nil }); err != nil {
		t.Fatal(err)
	}
	if !final.Done || len(final.Groups) != 0 || len(final.PendingSources) != 0 {
		t.Fatalf("final=%+v", final)
	}
}
func TestSearchStreamCancellationNotCached(t *testing.T) {
	c := newStreamClient()
	c.cancelled = make(chan struct{})
	c.started = make(chan struct{})
	svc := imports.NewService(c, nil, nil, "", time.Second, nil)
	imports.SetSearchCacheForTest(svc, constTTL(time.Minute), time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := svc.SearchStream(ctx, "alpha", nil, func(s imports.SearchSnapshotDTO) error {
		if len(s.Groups) > 0 {
			receiveSearch(t, c.started)
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	receiveSearch(t, c.cancelled)
	c.started = nil
	close(c.blocked)
	var snapshots []imports.SearchSnapshotDTO
	if err := svc.SearchStream(context.Background(), "alpha", nil, func(s imports.SearchSnapshotDTO) error { snapshots = append(snapshots, s); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) < 2 || len(snapshots[len(snapshots)-1].Groups) != 2 {
		t.Fatalf("cancelled result cached: %+v", snapshots)
	}
	snapshots = nil
	if err := svc.SearchStream(context.Background(), "alpha", nil, func(s imports.SearchSnapshotDTO) error { snapshots = append(snapshots, s); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || !snapshots[0].Done {
		t.Fatalf("cache hit=%+v", snapshots)
	}
}
func TestSearchDeadlineIncludesResolution(t *testing.T) {
	c := newStreamClient()
	c.blockSources = true
	svc := imports.NewService(c, nil, nil, "", 20*time.Millisecond, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	_, err := svc.Search(ctx, "alpha", nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 300*time.Millisecond {
		t.Fatalf("resolution deadline: %v after %v", err, time.Since(start))
	}
}
func TestSearchStreamConsumerFailureCancels(t *testing.T) {
	c := newStreamClient()
	svc := imports.NewService(c, nil, nil, "", time.Second, nil)
	sentinel := errors.New("writer unavailable")
	if err := svc.SearchStream(context.Background(), "alpha", nil, func(imports.SearchSnapshotDTO) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("err=%v", err)
	}
}

func TestSearchStreamDeadlineRetainsHealthyResults(t *testing.T) {
	c := newStreamClient()
	svc := imports.NewService(c, nil, nil, "", 30*time.Millisecond, nil)
	var final imports.SearchSnapshotDTO
	if err := svc.SearchStream(context.Background(), "alpha", nil, func(s imports.SearchSnapshotDTO) error { final = s; return nil }); err != nil {
		t.Fatal(err)
	}
	if !final.Done || len(final.Groups) != 1 || final.Groups[0].Title != "Alpha" || len(final.PendingSources) != 0 {
		t.Fatalf("final=%+v", final)
	}
}
func TestSearchStreamFailureRetainsHealthyResults(t *testing.T) {
	c := newStreamClient()
	close(c.blocked)
	c.fakeClient.searchErrs = map[int64]error{2: errors.New("upstream unavailable")}
	svc := imports.NewService(c, nil, nil, "", time.Second, nil)
	var final imports.SearchSnapshotDTO
	if err := svc.SearchStream(context.Background(), "alpha", nil, func(s imports.SearchSnapshotDTO) error { final = s; return nil }); err != nil {
		t.Fatal(err)
	}
	if !final.Done || len(final.Groups) != 1 || final.Groups[0].Title != "Alpha" {
		t.Fatalf("final=%+v", final)
	}
}

func TestSearchStreamSharesJSONCache(t *testing.T) {
	c := twoSourceClient()
	svc := imports.NewService(c, nil, nil, "", time.Second, nil)
	imports.SetSearchCacheForTest(svc, constTTL(time.Minute), time.Now)
	expected, err := svc.Search(context.Background(), "alpha", nil)
	if err != nil {
		t.Fatal(err)
	}
	var snapshots []imports.SearchSnapshotDTO
	if err = svc.SearchStream(context.Background(), "alpha", nil, func(s imports.SearchSnapshotDTO) error { snapshots = append(snapshots, s); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || !snapshots[0].Done || len(snapshots[0].Groups) != len(expected) || c.searchCount(1) != 1 || c.searchCount(2) != 1 {
		t.Fatalf("cache not shared: %+v calls=%d/%d", snapshots, c.searchCount(1), c.searchCount(2))
	}
}
func TestSearchStreamConsumerFailureNotCached(t *testing.T) {
	c := newStreamClient()
	svc := imports.NewService(c, nil, nil, "", time.Second, nil)
	imports.SetSearchCacheForTest(svc, constTTL(time.Minute), time.Now)
	sentinel := errors.New("write failed")
	err := svc.SearchStream(context.Background(), "alpha", nil, func(s imports.SearchSnapshotDTO) error {
		if len(s.Groups) > 0 {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v", err)
	}
	close(c.blocked)
	var final imports.SearchSnapshotDTO
	if err = svc.SearchStream(context.Background(), "alpha", nil, func(s imports.SearchSnapshotDTO) error { final = s; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(final.Groups) != 2 {
		t.Fatalf("failed writer poisoned cache: %+v", final)
	}
}
func TestSearchStreamSourceFilter(t *testing.T) {
	c := newStreamClient()
	svc := imports.NewService(c, nil, nil, "", time.Second, nil)
	var final imports.SearchSnapshotDTO
	if err := svc.SearchStream(context.Background(), "alpha", []string{"1"}, func(s imports.SearchSnapshotDTO) error {
		for _, p := range s.PendingSources {
			if p.ID != "1" {
				t.Fatalf("filter leaked source=%s", p.ID)
			}
		}
		final = s
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !final.Done || len(final.Groups) != 1 || final.Groups[0].Candidates[0].Source != "1" {
		t.Fatalf("final=%+v", final)
	}
}

func receiveSearch[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("search event withheld")
	}
	var zero T
	return zero
}

func receiveHealthySearch(t *testing.T, ch <-chan imports.SearchSnapshotDTO) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case value := <-ch:
			if len(value.Groups) > 0 {
				return
			}
		case <-deadline:
			t.Fatal("healthy search result withheld")
		}
	}
}

func assertCompletedSearch(t *testing.T, snapshots <-chan imports.SearchSnapshotDTO, wantGroups int) {
	t.Helper()
	var final imports.SearchSnapshotDTO
	for len(snapshots) > 0 {
		final = <-snapshots
	}
	if !final.Done || len(final.PendingSources) != 0 || len(final.Groups) != wantGroups {
		t.Fatalf("final=%+v", final)
	}
}
