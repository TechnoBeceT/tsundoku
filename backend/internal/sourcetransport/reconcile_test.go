package sourcetransport_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/ent"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

type runtimeApplierFunc func(context.Context, int64) error

func (f runtimeApplierFunc) ApplySourceRuntime(ctx context.Context, sourceID int64) error {
	return f(ctx, sourceID)
}

type lifecycleRuntimeApplier struct {
	coordinator *enginetopo.SourceRuntimeApplier
	apply       func(context.Context, int64) error
}

func (a lifecycleRuntimeApplier) ApplySourceRuntime(ctx context.Context, sourceID int64) error {
	return a.apply(ctx, sourceID)
}

func (a lifecycleRuntimeApplier) RunRuntime(ctx context.Context, run func(context.Context) error) error {
	return a.coordinator.RunRuntime(ctx, run)
}

func newRuntimeService(t *testing.T, applier sourcetransport.RuntimeApplier) (*sourcetransport.Service, *ent.Client) {
	t.Helper()
	client := testdb.New(t)
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})
	return svc.WithRuntimeApplier(applier), client
}

func TestUpdateAppliesCommittedRevisionSynchronously(t *testing.T) {
	var applied []int64
	svc, _ := newRuntimeService(t, runtimeApplierFunc(func(_ context.Context, sourceID int64) error {
		applied = append(applied, sourceID)
		return nil
	}))

	got, err := svc.Update(context.Background(), 101, sourcetransport.Patch{
		ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(applied) != 1 || applied[0] != 101 {
		t.Fatalf("runtime applications = %v, want [101]", applied)
	}
	if got.Intent.DesiredRevision != 1 || got.Intent.AppliedRevision != 1 || got.Intent.LastApplyAttempt == nil || got.Intent.LastApplyError != "" {
		t.Fatalf("intent after synchronous apply = %+v, want desired/applied revision 1", got.Intent)
	}
}

func TestApplyPendingFailureStaysPendingWithSanitizedError(t *testing.T) {
	svc, _ := newRuntimeService(t, runtimeApplierFunc(func(context.Context, int64) error {
		return errors.New("  profile fallback\r\n" + strings.Repeat("x", 600))
	}))

	got, err := svc.Update(context.Background(), 101, sourcetransport.Patch{
		ReuseBypassSession: sourcetransport.Set(false),
	})
	if err == nil || !strings.Contains(err.Error(), "profile fallback") {
		t.Fatalf("Update error = %v, want profile fallback", err)
	}
	if got.Intent.DesiredRevision != 1 || got.Intent.AppliedRevision != 0 || got.Intent.LastApplyAttempt == nil {
		t.Fatalf("intent after failed apply = %+v, want desired 1 / applied 0 with attempt", got.Intent)
	}
	if got.Intent.LastApplyError == "" || len(got.Intent.LastApplyError) > 512 || strings.ContainsAny(got.Intent.LastApplyError, "\r\n") {
		t.Fatalf("stored apply error = %q, want sanitized and bounded", got.Intent.LastApplyError)
	}
}

func TestApplyPendingCannotAcknowledgeRevisionCreatedDuringApply(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	plain := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})
	var callbackErr error
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(context.Context, int64) error {
			_, callbackErr = plain.Update(ctx, 101, sourcetransport.Patch{
				ReuseBypassSession: sourcetransport.Set(false),
			})
			return nil
		}))

	got, err := svc.Update(ctx, 101, sourcetransport.Patch{
		ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse),
	})
	if err != nil {
		t.Fatalf("first Update: %v", err)
	}
	if callbackErr != nil {
		t.Fatalf("newer Update during apply: %v", callbackErr)
	}
	if got.Intent.DesiredRevision != 1 || got.Intent.AppliedRevision != 0 {
		t.Fatalf("first update result intent = %+v, want its coherent revision 1 still unacknowledged", got.Intent)
	}
	pending, err := svc.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending after revision race: %v", err)
	}
	if len(pending) != 1 || pending[0].DesiredRevision != 2 || pending[0].AppliedRevision != 0 {
		t.Fatalf("stored intent after revision race = %+v, want newer desired 2 still pending", pending)
	}
}

func TestCanceledSourceApplyPersistsBoundedAttemptMetadata(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	plain := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})
	if _, err := plain.Update(ctx, 202, sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(false)}); err != nil {
		t.Fatalf("persist pending source revision: %v", err)
	}

	started := make(chan struct{})
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(ctx context.Context, _ int64) error {
			close(started)
			<-ctx.Done()
			return errors.New(" source cancelled\r\n" + strings.Repeat("x", 600))
		}))
	applyCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := svc.ApplyPending(applyCtx, 202); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ApplyPending error = nil, want cancellation failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ApplyPending did not finish within detached metadata bound")
	}

	pending, err := svc.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].DesiredRevision != 1 || pending[0].AppliedRevision != 0 || pending[0].LastApplyAttempt == nil {
		t.Fatalf("pending after canceled apply = %+v, want revision 1 with attempt", pending)
	}
	if pending[0].LastApplyError == "" || len(pending[0].LastApplyError) > 512 || strings.ContainsAny(pending[0].LastApplyError, "\r\n") {
		t.Fatalf("last apply error = %q, want sanitized bounded metadata", pending[0].LastApplyError)
	}
}

func TestCanceledSourceApplyCannotHoldShutdownOnMetadataWrite(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	plain := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})
	if _, err := plain.Update(ctx, 204, sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(false)}); err != nil {
		t.Fatalf("persist pending source revision: %v", err)
	}
	client.SourceRuntimeIntent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if m, ok := mutation.(*ent.SourceRuntimeIntentMutation); ok {
				if _, metadataWrite := m.LastApplyError(); metadataWrite {
					<-ctx.Done()
					return nil, ctx.Err()
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})

	started := make(chan struct{})
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(ctx context.Context, _ int64) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}))
	applyCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := svc.ApplyPending(applyCtx, 204); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ApplyPending error = nil, want canceled apply and metadata timeout")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("ApplyPending held shutdown for %v, want bounded under 2s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ApplyPending held shutdown beyond detached metadata timeout")
	}
	pending, err := svc.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].DesiredRevision != 1 || pending[0].AppliedRevision != 0 {
		t.Fatalf("pending after metadata timeout = %+v, want revision 1 pending", pending)
	}
}

func TestRuntimeConvergenceShutdownJoinsSourceMetadataTail(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	plain := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})
	if _, err := plain.Update(ctx, 205, sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(false)}); err != nil {
		t.Fatalf("persist pending source revision: %v", err)
	}
	metadataEntered := make(chan struct{})
	releaseMetadata := make(chan struct{})
	client.SourceRuntimeIntent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if m, ok := mutation.(*ent.SourceRuntimeIntentMutation); ok {
				if _, metadataWrite := m.LastApplyError(); metadataWrite {
					close(metadataEntered)
					<-releaseMetadata
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})

	coordinator := enginetopo.NewSourceRuntimeApplier(sourceenginefake.New(), enginetopo.NetworkReconcileDeps{})
	applyEntered := make(chan struct{})
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(lifecycleRuntimeApplier{
			coordinator: coordinator,
			apply: func(ctx context.Context, _ int64) error {
				close(applyEntered)
				<-ctx.Done()
				return ctx.Err()
			},
		})
	applyDone := make(chan error, 1)
	go func() {
		_, err := svc.ApplyPending(ctx, 205)
		applyDone <- err
	}()
	<-applyEntered

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- coordinator.ShutdownRuntimeConvergence(context.Background()) }()
	select {
	case <-metadataEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("source apply did not enter detached metadata tail during convergence shutdown")
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("convergence shutdown returned before source metadata tail: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseMetadata)
	select {
	case err := <-applyDone:
		if err == nil {
			t.Fatal("ApplyPending error = nil, want lifecycle cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("source ApplyPending did not finish after metadata release")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("ShutdownRuntimeConvergence: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("convergence shutdown did not join source metadata tail")
	}
}

func TestFailedSourceApplyDoesNotOverwriteNewerRevisionMetadata(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	plain := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})
	if _, err := plain.Update(ctx, 203, sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(false)}); err != nil {
		t.Fatalf("persist first source revision: %v", err)
	}

	var newerErr error
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(context.Context, int64) error {
			_, newerErr = plain.Update(ctx, 203, sourcetransport.Patch{
				ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse),
			})
			return errors.New("obsolete source revision failed")
		}))
	if _, err := svc.ApplyPending(ctx, 203); err == nil {
		t.Fatal("ApplyPending error = nil, want obsolete apply failure")
	}
	if newerErr != nil {
		t.Fatalf("persist newer source revision: %v", newerErr)
	}
	pending, err := svc.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].DesiredRevision != 2 || pending[0].AppliedRevision != 0 || pending[0].LastApplyAttempt != nil || pending[0].LastApplyError != "" {
		t.Fatalf("newer intent metadata = %+v, want untouched pending revision 2", pending)
	}
}

func TestReconcilePendingRetriesPersistedIntentAtStartup(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	beforeRestart := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})
	if _, err := beforeRestart.Update(ctx, 303, sourcetransport.Patch{
		ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse),
	}); err != nil {
		t.Fatalf("persist pending policy: %v", err)
	}

	var mu sync.Mutex
	var applied []int64
	afterRestart := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(_ context.Context, sourceID int64) error {
			mu.Lock()
			applied = append(applied, sourceID)
			mu.Unlock()
			return nil
		}))
	if err := afterRestart.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending: %v", err)
	}
	pending, err := afterRestart.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after startup retry = %+v, want none", pending)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(applied) != 1 || applied[0] != 303 {
		t.Fatalf("startup applications = %v, want [303]", applied)
	}
}

func TestConcurrentApplyPendingCoalescesAlreadyAppliedRevision(t *testing.T) {
	ctx := context.Background()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int
	var mu sync.Mutex

	// Persist without invoking the configured applier, then race two explicit
	// retries. The second caller must re-read under the apply latch and observe
	// that the first already acknowledged the revision.
	client := testdb.New(t)
	base := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})
	if _, err := base.Update(ctx, 404, sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(false)}); err != nil {
		t.Fatalf("persist pending policy: %v", err)
	}
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(context.Context, int64) error {
			mu.Lock()
			calls++
			first := calls == 1
			mu.Unlock()
			if first {
				close(started)
				<-release
			}
			return nil
		}))

	errs := make(chan error, 2)
	go func() { _, err := svc.ApplyPending(ctx, 404); errs <- err }()
	<-started
	go func() { _, err := svc.ApplyPending(ctx, 404); errs <- err }()
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("ApplyPending: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("runtime apply calls = %d, want 1 coalesced call", calls)
	}
}

func TestApplyPendingQueuedWaitHonorsContextCancellation(t *testing.T) {
	ctx := context.Background()
	started := make(chan struct{})
	release := make(chan struct{})
	client := testdb.New(t)
	base := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})
	if _, err := base.Update(ctx, 505, sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(false)}); err != nil {
		t.Fatalf("persist pending policy: %v", err)
	}
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(context.Context, int64) error {
			close(started)
			<-release
			return nil
		}))
	firstDone := make(chan error, 1)
	go func() { _, err := svc.ApplyPending(ctx, 505); firstDone <- err }()
	<-started

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	secondDone := make(chan error, 1)
	go func() { _, err := svc.ApplyPending(cancelled, 505); secondDone <- err }()
	select {
	case err := <-secondDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued ApplyPending error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		<-firstDone
		<-secondDone
		t.Fatal("queued ApplyPending did not return while the runtime serializer was occupied")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first ApplyPending: %v", err)
	}
}
