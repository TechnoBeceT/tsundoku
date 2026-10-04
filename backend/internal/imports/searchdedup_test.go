package imports_test

import (
	"context"
	"errors"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type dedupClient struct {
	*fakeClient
	calls     atomic.Int32
	slow      chan struct{}
	cancelled chan struct{}
}

func (c *dedupClient) Search(ctx context.Context, id int64, q string, p int) (sourceengine.SearchResult, error) {
	c.calls.Add(1)
	if id == 2 {
		select {
		case <-c.slow:
		case <-ctx.Done():
			select {
			case c.cancelled <- struct{}{}:
			default:
			}
			return sourceengine.SearchResult{}, ctx.Err()
		}
	}
	return c.fakeClient.Search(ctx, id, q, p)
}
func dedupService(ttl time.Duration) (*imports.Service, *dedupClient) {
	c := &dedupClient{fakeClient: newStreamClient().fakeClient, slow: make(chan struct{}), cancelled: make(chan struct{}, 10)}
	s := imports.NewService(c, nil, nil, "", time.Second, nil)
	imports.SetSearchCacheForTest(s, constTTL(ttl), time.Now)
	return s, c
}
func TestSearchDedupLateJoinAndIndependentCancellation(t *testing.T) {
	for _, ttl := range []time.Duration{time.Minute, 0} {
		t.Run(ttl.String(), func(t *testing.T) {
			checkLateJoinCancellation(t, ttl)
		})
	}
}
func TestSearchDedupLastDepartureCancelsWithoutCaching(t *testing.T) {
	s, c := dedupService(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshots := make(chan imports.SearchSnapshotDTO, 8)
	done := make(chan error, 1)
	go func() {
		done <- s.SearchStream(ctx, "alpha", nil, func(v imports.SearchSnapshotDTO) error { snapshots <- v; return nil })
	}()
	receiveHealthySearch(t, snapshots)
	// A healthy snapshot does not prove the slow source has entered its call.
	waitSearchCalls(t, c, 2)
	cancel()
	<-done
	select {
	case <-c.cancelled:
	case <-time.After(time.Second):
		t.Fatal("last departure kept upstream alive")
	}
	close(c.slow)
	groups, err := s.Search(context.Background(), "alpha", nil)
	if err != nil || len(groups) != 2 || c.calls.Load() != 4 {
		t.Fatalf("cancelled work cached: groups=%v err=%v calls=%d", groups, err, c.calls.Load())
	}
}

func TestSearchDedupSlowWriterDoesNotBlockJSONOrOtherKey(t *testing.T) {
	s, c := dedupService(time.Minute)
	writerEntered := make(chan struct{})
	releaseWriter := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseWriter) }) }
	defer release()
	streamDone := make(chan error, 1)
	go stalledSearchWriter(s, writerEntered, releaseWriter, streamDone)
	receiveSearch(t, writerEntered)
	// Different source-set/query does not queue behind this key or its writer.
	if groups, err := s.Search(context.Background(), "different", []string{"1"}); err != nil || len(groups) != 1 {
		t.Fatalf("other key=%v %v", groups, err)
	}
	jsonDone := make(chan error, 1)
	go searchJSONForWriterTest(s, jsonDone)
	// A late stream establishes that shared demand exists before releasing slow work.
	joined := make(chan struct{})
	joinedDone := make(chan error, 1)
	go func() {
		first := true
		joinedDone <- s.SearchStream(context.Background(), "alpha", nil, func(v imports.SearchSnapshotDTO) error {
			if first {
				first = false
				close(joined)
			}
			return nil
		})
	}()
	<-joined
	close(c.slow)
	if err := receiveSearch(t, jsonDone); err != nil {
		t.Fatal(err)
	}
	if err := <-joinedDone; err != nil {
		t.Fatal(err)
	}
	if n := c.calls.Load(); n != 3 {
		t.Fatalf("shared/mixed fanout calls=%d want 3", n)
	}
	release()
	if err := <-streamDone; err == nil {
		t.Fatal("writer failure lost")
	}
	// Successful JSON demand caches independently of the stream's eventual writer failure.
	if _, err := s.Search(context.Background(), "alpha", nil); err != nil || c.calls.Load() != 3 {
		t.Fatalf("completed shared result not cached: %v", err)
	}
}

func TestSearchDedupJSONLeaderCancellationKeepsStreamDemand(t *testing.T) {
	s, c := dedupService(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jsonDone := make(chan error, 1)
	go func() { _, err := s.Search(ctx, "alpha", nil); jsonDone <- err }()
	// Ensure the JSON leader has admitted source fanout before the stream joins.
	waitSearchCalls(t, c, 2)
	partial := make(chan imports.SearchSnapshotDTO, 8)
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- s.SearchStream(context.Background(), "alpha", nil, func(v imports.SearchSnapshotDTO) error { partial <- v; return nil })
	}()
	receiveHealthySearch(t, partial)
	cancel()
	if err := <-jsonDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(c.slow)
	if err := <-streamDone; err != nil {
		t.Fatal(err)
	}
	if c.calls.Load() != 2 {
		t.Fatalf("JSON/stream duplicated fanout: %d", c.calls.Load())
	}
}

func checkLateJoinCancellation(t *testing.T, ttl time.Duration) {
	t.Helper()
	s, c := dedupService(ttl)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan imports.SearchSnapshotDTO, 8)
	done := make(chan error, 1)
	go func() {
		done <- s.SearchStream(ctx, " Alpha ", []string{"2", "1"}, func(v imports.SearchSnapshotDTO) error { first <- v; return nil })
	}()
	receiveHealthySearch(t, first)
	// Establish both physical source calls before testing shared-demand cancellation.
	waitSearchCalls(t, c, 2)
	joined := make(chan imports.SearchSnapshotDTO, 8)
	second := make(chan error, 1)
	go func() {
		second <- s.SearchStream(context.Background(), "alpha", []string{"1", "2"}, func(v imports.SearchSnapshotDTO) error { joined <- v; return nil })
	}()
	v := receiveSearch(t, joined)
	if len(v.Groups) != 1 || v.Done || len(v.PendingSources) != 1 {
		t.Fatalf("late join lost healthy snapshot: %+v", v)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if n := c.calls.Load(); n != 2 {
		t.Fatalf("duplicate fanout calls=%d want 2", n)
	}
	select {
	case <-c.cancelled:
		t.Fatal("one caller cancelled another's work")
	default:
	}
	close(c.slow)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

func waitSearchCalls(t *testing.T, c *dedupClient, want int32) {
	t.Helper()
	limit := time.After(time.Second)
	for c.calls.Load() < want {
		select {
		case <-limit:
			t.Fatal("search fanout did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func stalledSearchWriter(s *imports.Service, writerEntered chan struct{}, releaseWriter <-chan struct{}, streamDone chan<- error) {
	streamDone <- s.SearchStream(context.Background(), "alpha", nil, func(v imports.SearchSnapshotDTO) error {
		if len(v.Groups) > 0 && !v.Done {
			close(writerEntered)
			<-releaseWriter
			return errors.New("writer failed")
		}
		return nil
	})
}

func searchJSONForWriterTest(s *imports.Service, jsonDone chan<- error) {
	groups, err := s.Search(context.Background(), "alpha", nil)
	if err == nil && len(groups) != 2 {
		err = errors.New("missing completed groups")
	}
	jsonDone <- err
}
