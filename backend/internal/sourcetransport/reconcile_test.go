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
	entintent "github.com/technobecet/tsundoku/internal/ent/sourceruntimeintent"
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

type countedLifecycleRuntimeApplier struct {
	admissions int
	applies    []int64
}

func (a *countedLifecycleRuntimeApplier) ApplySourceRuntime(_ context.Context, sourceID int64) error {
	a.applies = append(a.applies, sourceID)
	return nil
}

func (a *countedLifecycleRuntimeApplier) RunRuntime(ctx context.Context, run func(context.Context) error) error {
	a.admissions++
	return run(ctx)
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

func TestApplyRevisionNeverAppliesOrAcknowledgesAnotherCommittedRevision(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	plain := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})
	for _, patch := range []sourcetransport.Patch{
		{ReuseBypassSession: sourcetransport.Set(false)},
		{ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse)},
	} {
		if _, err := plain.Update(ctx, 111, patch); err != nil {
			t.Fatalf("persist revision: %v", err)
		}
	}
	applied := 0
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(context.Context, int64) error {
			applied++
			return nil
		}))

	got, err := svc.ApplyRevision(ctx, 111, 1)
	if err != nil {
		t.Fatalf("ApplyRevision obsolete revision: %v", err)
	}
	if applied != 0 {
		t.Fatalf("runtime applies = %d, want 0 for obsolete committed revision", applied)
	}
	if got.DesiredRevision != 2 || got.AppliedRevision != 0 {
		t.Fatalf("intent = %+v, want newer revision 2 pending", got)
	}
}

func TestApplyRevisionsConvergesManyExactPendingRevisionsOnceInStableOrder(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedRuntimeIntent(t, client, 3, 2, 2)
	seedRuntimeIntent(t, client, 2, 7, 4)
	seedRuntimeIntent(t, client, 1, 3, 1)

	var applied []int64
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(_ context.Context, sourceID int64) error {
			applied = append(applied, sourceID)
			return nil
		}))
	got, err := svc.ApplyRevisions(ctx, []sourcetransport.Intent{
		{SourceID: 3, DesiredRevision: 2},
		{SourceID: 2, DesiredRevision: 7},
		{SourceID: 1, DesiredRevision: 3},
	})
	if err != nil {
		t.Fatalf("ApplyRevisions: %v", err)
	}
	if len(applied) != 1 || applied[0] != 1 {
		t.Fatalf("runtime applications = %v, want one stable source-1 diagnostic apply", applied)
	}
	if len(got) != 3 {
		t.Fatalf("applied intents = %+v, want 3 source-ordered current revisions", got)
	}
	assertIntentRevision(t, got[0], 1, 3, 3)
	assertIntentRevision(t, got[1], 2, 7, 7)
	assertIntentRevision(t, got[2], 3, 2, 2)
}

func TestApplyRevisionsUsesOneSharedRuntimeLifecycleAdmission(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedRuntimeIntent(t, client, 7, 4, 0)
	seedRuntimeIntent(t, client, 8, 9, 3)
	applier := &countedLifecycleRuntimeApplier{}
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(applier)

	if _, err := svc.ApplyRevisions(ctx, []sourcetransport.Intent{
		{SourceID: 8, DesiredRevision: 9},
		{SourceID: 7, DesiredRevision: 4},
	}); err != nil {
		t.Fatalf("ApplyRevisions: %v", err)
	}
	if applier.admissions != 1 || len(applier.applies) != 1 || applier.applies[0] != 7 {
		t.Fatalf("runtime lifecycle: admissions=%d applies=%v, want one admission and one stable apply", applier.admissions, applier.applies)
	}
}

func TestApplyRevisionsSkipsStalePairsButConvergesAndAcknowledgesExactPeers(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedRuntimeIntent(t, client, 11, 2, 0)
	seedRuntimeIntent(t, client, 12, 1, 0)

	var applied []int64
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(_ context.Context, sourceID int64) error {
			applied = append(applied, sourceID)
			return nil
		}))
	got, err := svc.ApplyRevisions(ctx, []sourcetransport.Intent{
		{SourceID: 12, DesiredRevision: 1},
		{SourceID: 11, DesiredRevision: 1},
	})
	if err != nil {
		t.Fatalf("ApplyRevisions: %v", err)
	}
	if len(applied) != 1 || applied[0] != 12 {
		t.Fatalf("runtime applications = %v, want one exact source-12 apply", applied)
	}
	if len(got) != 2 {
		t.Fatalf("current intents = %+v, want stale source 11 and exact source 12", got)
	}
	assertIntentRevision(t, got[0], 11, 2, 0)
	if got[0].LastApplyAttempt != nil {
		t.Fatalf("stale intent attempt = %v, want untouched", got[0].LastApplyAttempt)
	}
	assertIntentRevision(t, got[1], 12, 1, 1)
}

func TestApplyRevisionsExactGuardsEachAcknowledgementAgainstMidApplyStaleness(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedRuntimeIntent(t, client, 21, 2, 0)
	seedRuntimeIntent(t, client, 22, 5, 1)

	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(context.Context, int64) error {
			client.SourceRuntimeIntent.Update().
				Where(entintent.SourceID(21)).
				AddDesiredRevision(1).
				ExecX(ctx)
			return nil
		}))
	got, err := svc.ApplyRevisions(ctx, []sourcetransport.Intent{
		{SourceID: 21, DesiredRevision: 2},
		{SourceID: 22, DesiredRevision: 5},
	})
	if err != nil {
		t.Fatalf("ApplyRevisions: %v", err)
	}
	if len(got) != 2 || got[0].DesiredRevision != 3 || got[0].AppliedRevision != 0 || got[0].LastApplyAttempt != nil || got[1].DesiredRevision != 5 || got[1].AppliedRevision != 5 {
		t.Fatalf("current intents = %+v, want newer source 21 pending and source 22 acknowledged", got)
	}
}

func TestApplyRevisionsDoesNotRecreateIntentDeletedDuringConvergence(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedRuntimeIntent(t, client, 23, 2, 0)
	seedRuntimeIntent(t, client, 24, 5, 1)

	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(context.Context, int64) error {
			client.SourceRuntimeIntent.Delete().Where(entintent.SourceID(23)).ExecX(ctx)
			return nil
		}))
	got, err := svc.ApplyRevisions(ctx, []sourcetransport.Intent{
		{SourceID: 23, DesiredRevision: 2},
		{SourceID: 24, DesiredRevision: 5},
	})
	if err != nil {
		t.Fatalf("ApplyRevisions: %v", err)
	}
	if client.SourceRuntimeIntent.Query().Where(entintent.SourceID(23)).ExistX(ctx) {
		t.Fatal("deleted source 23 intent was recreated by a stale acknowledgement")
	}
	if len(got) != 2 {
		t.Fatalf("current intents = %+v, want missing source 23 and exact source 24", got)
	}
	assertIntentRevision(t, got[0], 23, 0, 0)
	assertIntentRevision(t, got[1], 24, 5, 5)
}

func TestApplyRevisionsFailureAndCancellationLeaveEveryExactRevisionPending(t *testing.T) {
	for _, tc := range []struct {
		name         string
		apply        func(context.Context) error
		cancelDuring bool
	}{
		{name: "failure", apply: func(context.Context) error { return errors.New(" engine failed\r\nretry ") }},
		{
			name:         "cancellation",
			apply:        func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
			cancelDuring: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertApplyRevisionsFailure(t, tc.apply, tc.cancelDuring)
		})
	}
}

func assertApplyRevisionsFailure(t *testing.T, apply func(context.Context) error, cancelDuring bool) {
	t.Helper()
	ctx := context.Background()
	client := testdb.New(t)
	seedRuntimeIntent(t, client, 31, 4, 1)
	seedRuntimeIntent(t, client, 32, 8, 2)
	started := make(chan struct{})
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
		WithRuntimeApplier(runtimeApplierFunc(func(ctx context.Context, _ int64) error {
			close(started)
			return apply(ctx)
		}))

	applyCtx := ctx
	cancel := func() {}
	if cancelDuring {
		applyCtx, cancel = context.WithCancel(ctx)
	}
	done := make(chan error, 1)
	go func() {
		_, err := svc.ApplyRevisions(applyCtx, []sourcetransport.Intent{
			{SourceID: 32, DesiredRevision: 8},
			{SourceID: 31, DesiredRevision: 4},
		})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("ApplyRevisions error = nil, want convergence failure")
	}

	rows := client.SourceRuntimeIntent.Query().Order(entintent.BySourceID()).AllX(ctx)
	if len(rows) != 2 {
		t.Fatalf("runtime intents = %d, want 2", len(rows))
	}
	for _, row := range rows {
		assertFailedRuntimeIntent(t, row)
	}
}

func assertFailedRuntimeIntent(t *testing.T, row *ent.SourceRuntimeIntent) {
	t.Helper()
	if row.AppliedRevision >= row.DesiredRevision || row.LastApplyAttempt == nil ||
		row.LastApplyError == "" || strings.ContainsAny(row.LastApplyError, "\r\n") {
		t.Fatalf("failed intent = %+v, want pending with sanitized attempt metadata", row)
	}
}

func TestApplyRevisionsEmptyBatchDoesNoRuntimeWork(t *testing.T) {
	called := false
	svc, _ := newRuntimeService(t, runtimeApplierFunc(func(context.Context, int64) error {
		called = true
		return nil
	}))
	got, err := svc.ApplyRevisions(context.Background(), nil)
	if err != nil {
		t.Fatalf("ApplyRevisions: %v", err)
	}
	if called || len(got) != 0 {
		t.Fatalf("empty batch: called=%v intents=%+v, want no work", called, got)
	}
}

func seedRuntimeIntent(t *testing.T, client *ent.Client, sourceID, desired, applied int64) {
	t.Helper()
	client.SourceRuntimeIntent.Create().
		SetSourceID(sourceID).
		SetDesiredRevision(desired).
		SetAppliedRevision(applied).
		ExecX(context.Background())
}

func assertIntentRevision(t *testing.T, got sourcetransport.Intent, sourceID, desired, applied int64) {
	t.Helper()
	if got.SourceID != sourceID || got.DesiredRevision != desired || got.AppliedRevision != applied {
		t.Fatalf("intent = %+v, want source %d revision %d/%d", got, sourceID, applied, desired)
	}
}

func TestCanceledSourceApplyPersistsBoundedAttemptMetadata(t *testing.T) { //nolint:cyclop // Cancellation test asserts every bounded metadata field.
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

func TestCanceledSourceApplyCannotHoldShutdownOnMetadataWrite(t *testing.T) { //nolint:gocognit,cyclop // Concurrency timeline needs explicit cancellation and timeout diagnostics.
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

func TestRuntimeConvergenceShutdownJoinsSourceMetadataTail(t *testing.T) { //nolint:gocognit,cyclop // Shutdown test observes both convergence and metadata tails.
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
