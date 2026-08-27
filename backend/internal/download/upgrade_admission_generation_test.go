package download

import (
	"context"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

type upgradeAdmissionWaiter struct {
	sourceKey string
	entered   chan struct{}
	release   <-chan struct{}
	once      sync.Once
}

func (w *upgradeAdmissionWaiter) Wait(ctx context.Context, sourceKey string) {
	if sourceKey != w.sourceKey {
		return
	}
	w.once.Do(func() { close(w.entered) })
	select {
	case <-w.release:
	case <-ctx.Done():
	}
}

type upgradeAdmissionRecorder struct {
	calls atomic.Int32
}

func (r *upgradeAdmissionRecorder) Log(context.Context, sourceevents.Event) {
	r.calls.Add(1)
}

func (r *upgradeAdmissionRecorder) LogBatch(_ context.Context, events []sourceevents.Event) {
	for range events {
		r.calls.Add(1)
	}
}

type upgradeAdmissionCase struct {
	name   string
	mutate func(context.Context, *ent.Client, *ent.ProviderChapter, time.Time)
}

func upgradeAdmissionCases() []upgradeAdmissionCase {
	return []upgradeAdmissionCase{
		{
			name: "generation",
			mutate: func(ctx context.Context, client *ent.Client, pc *ent.ProviderChapter, _ time.Time) {
				client.ProviderChapter.UpdateOneID(pc.ID).SetURL("https://high.example.test/replaced").ExecX(ctx)
			},
		},
		{
			name: "retry_budget",
			mutate: func(ctx context.Context, client *ent.Client, pc *ent.ProviderChapter, _ time.Time) {
				client.ProviderChapter.UpdateOneID(pc.ID).SetAttempts(3).ExecX(ctx)
			},
		},
		{
			name: "cooldown",
			mutate: func(ctx context.Context, client *ent.Client, pc *ent.ProviderChapter, now time.Time) {
				client.ProviderChapter.UpdateOneID(pc.ID).SetNextAttemptAt(now.Add(time.Hour)).ExecX(ctx)
			},
		},
		{
			name: "chapter_carrier",
			mutate: func(ctx context.Context, client *ent.Client, pc *ent.ProviderChapter, _ time.Time) {
				client.ProviderChapter.UpdateOneID(pc.ID).SetChapterKey("other").ExecX(ctx)
			},
		},
		{
			name: "breaker",
			mutate: func(ctx context.Context, client *ent.Client, _ *ent.ProviderChapter, now time.Time) {
				client.SourceCircuitState.Create().
					SetSourceKey("source-high").
					SetConsecutiveFailures(1).
					SetCooldownUntil(now.Add(time.Hour)).
					SetLastError("tripped while upgrade waited").
					SaveX(ctx)
			},
		},
	}
}

func TestUpgradeAll_AdmissionRevalidatesCurrentCandidateEligibility(t *testing.T) {
	for _, tt := range upgradeAdmissionCases() {
		t.Run(tt.name, func(t *testing.T) {
			testUpgradeAdmissionRevalidation(t, tt)
		})
	}
}

func testUpgradeAdmissionRevalidation(t *testing.T, tt upgradeAdmissionCase) {
	t.Helper()
	ctx := context.Background()
	client := testdb.New(t)
	fixture := seedConcurrentUpgrade(t, client, "upgrade-admission-"+tt.name)
	highPC := client.ProviderChapter.UpdateOneID(fixture.highPC.ID).
		SetPageLinks(admissionPageLinks()).
		SaveX(ctx)
	now := time.Now()
	release := make(chan struct{})
	waiter := &upgradeAdmissionWaiter{
		sourceKey: "source-high",
		entered:   make(chan struct{}),
		release:   release,
	}
	fetcher := &admissionFetcher{err: errClaimTestFetch}
	recorder := &upgradeAdmissionRecorder{}
	hub := sse.NewHub()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	stagingRoot := t.TempDir()
	policy := admissionPolicy()
	gate := sourcegate.NewService(client, policy)
	dispatcher := New(client, fetcher, hub, Config{Storage: t.TempDir(), StagingRoot: stagingRoot}, policy, gate).
		WithEventRecorder(recorder)
	dispatcher.waiter = waiter

	done := make(chan upgradeCall, 1)
	go func() {
		count, err := dispatcher.UpgradeAll(ctx, nil, nil)
		done <- upgradeCall{count: count, err: err}
	}()
	awaitUpgradeAdmissionWait(t, waiter.entered)

	tt.mutate(ctx, client, highPC, now)
	mutatedPC := client.ProviderChapter.GetX(ctx, highPC.ID)
	breakerBefore := upgradeAdmissionBreakerSnapshot(t, gate)
	close(release)
	assertUpgradeCycleCall(t, <-done, 0)

	assertLostUpgradeAdmission(t, client, fixture, highPC, mutatedPC, fetcher, recorder, breakerBefore, gate)
	assertNoUpgradeAdmissionArtifacts(t, events, stagingRoot)
}

func awaitUpgradeAdmissionWait(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(admissionTestTimeout):
		t.Fatal("upgrade did not reach the admission wait")
	}
}

func upgradeAdmissionBreakerSnapshot(t *testing.T, gate *sourcegate.Service) map[string]sourcegate.BreakerState {
	t.Helper()
	snapshot, err := gate.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot breaker: %v", err)
	}
	return snapshot
}

func assertLostUpgradeAdmission(
	t *testing.T,
	client *ent.Client,
	fixture concurrentUpgradeFixture,
	highPC, mutatedPC *ent.ProviderChapter,
	fetcher *admissionFetcher,
	recorder *upgradeAdmissionRecorder,
	breakerBefore map[string]sourcegate.BreakerState,
	gate *sourcegate.Service,
) {
	t.Helper()
	if got := fetcher.calls.Load(); got != 0 {
		t.Fatalf("engine calls = %d, want 0 after candidate eligibility changed", got)
	}
	gotChapter := client.Chapter.GetX(context.Background(), fixture.chapter.ID)
	if gotChapter.State != entchapter.StateUpgradeAvailable || gotChapter.LastError != "" {
		t.Fatalf("chapter state/error = %s/%q, want upgrade_available with no error", gotChapter.State, gotChapter.LastError)
	}
	assertProviderChapterAdmissionSnapshot(t, mutatedPC, client.ProviderChapter.GetX(context.Background(), highPC.ID))
	breakerAfter := upgradeAdmissionBreakerSnapshot(t, gate)
	if !reflect.DeepEqual(breakerBefore, breakerAfter) {
		t.Fatalf("breaker changed without an engine attempt: before=%+v after=%+v", breakerBefore, breakerAfter)
	}
	if got := recorder.calls.Load(); got != 0 {
		t.Fatalf("source events = %d, want 0 without an engine attempt", got)
	}
}

func assertNoUpgradeAdmissionArtifacts(t *testing.T, events <-chan sse.Event, stagingRoot string) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected SSE event without an engine attempt: %s", event.Type)
	default:
	}
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		t.Fatalf("read staging root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries = %d, want 0 without an engine attempt", len(entries))
	}
}

func assertProviderChapterAdmissionSnapshot(t *testing.T, want, got *ent.ProviderChapter) {
	t.Helper()
	if got.Attempts != want.Attempts || got.URL != want.URL || got.ChapterKey != want.ChapterKey ||
		!sameOptionalTime(got.NextAttemptAt, want.NextAttemptAt) || !reflect.DeepEqual(got.PageLinks, want.PageLinks) ||
		got.LastError != want.LastError {
		t.Fatalf("provider chapter changed after lost admission: want=%+v got=%+v", want, got)
	}
}
