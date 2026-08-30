package sourcetransport_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entintent "github.com/technobecet/tsundoku/internal/ent/sourceruntimeintent"
	entpolicy "github.com/technobecet/tsundoku/internal/ent/sourcetransportpolicy"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

var errUnavailableSource = errors.New("live source catalog unavailable")

type fakeCatalog struct{ err error }

func (c fakeCatalog) RequireSource(context.Context, int64) error { return c.err }

type fakeDefaults struct {
	image   sourcetransport.ImageConnectionMode
	resolve func(context.Context, int64, *bool) (bool, sourcetransport.BypassSessionMode, error)
}

func (d fakeDefaults) ImageConnectionMode(context.Context) sourcetransport.ImageConnectionMode {
	return d.image
}

func (d fakeDefaults) ResolveBypassSession(ctx context.Context, sourceID int64, override *bool) (bool, sourcetransport.BypassSessionMode, error) {
	if d.resolve != nil {
		return d.resolve(ctx, sourceID, override)
	}
	if override != nil && *override {
		return true, sourcetransport.BypassSessionReusable, nil
	}
	return false, sourcetransport.BypassSessionDisabled, nil
}

func newService(t *testing.T) (*sourcetransport.Service, *ent.Client) {
	t.Helper()
	client := testdb.New(t)
	return sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}), client
}

func TestResolveInheritsFreshDefaultsWithoutPolicy(t *testing.T) {
	svc, _ := newService(t)

	got, err := svc.Resolve(context.Background(), 101)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ReuseBypassSession || got.BypassSessionMode != sourcetransport.BypassSessionDisabled || got.ImageConnectionMode != sourcetransport.ImageConnectionFresh {
		t.Fatalf("Resolve = %+v, want inherited disabled/fresh defaults", got)
	}
}

func TestUpdateStoresExplicitFalse(t *testing.T) {
	svc, _ := newService(t)

	got, err := svc.Update(context.Background(), 101, sourcetransport.Patch{
		ReuseBypassSession: sourcetransport.Set(false),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Override.ReuseBypassSession == nil || *got.Override.ReuseBypassSession {
		t.Fatalf("stored override = %+v, want explicit false", got.Override)
	}
	if got.Effective.ReuseBypassSession || got.Effective.BypassSessionMode != sourcetransport.BypassSessionDisabled {
		t.Fatalf("effective policy = %+v, want disabled", got.Effective)
	}
	if got.Intent.DesiredRevision != 1 || got.Intent.AppliedRevision != 0 {
		t.Fatalf("intent = %+v, want desired 1 and applied 0", got.Intent)
	}
}

func TestUpdateClearFinalPolicyReturnsToInheritanceAndKeepsIntent(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	if _, err := svc.Update(ctx, 101, sourcetransport.Patch{
		ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse),
	}); err != nil {
		t.Fatalf("set override: %v", err)
	}
	got, err := svc.Update(ctx, 101, sourcetransport.Patch{
		ImageConnectionMode: sourcetransport.Clear[sourcetransport.ImageConnectionMode](),
	})
	if err != nil {
		t.Fatalf("clear override: %v", err)
	}
	if got.Override.ImageConnectionMode != nil || got.Effective.ImageConnectionMode != sourcetransport.ImageConnectionFresh {
		t.Fatalf("clear result = %+v, want inherited fresh mode", got)
	}
	if got.Intent.DesiredRevision != 2 {
		t.Fatalf("desired revision = %d, want 2", got.Intent.DesiredRevision)
	}
	if n := client.SourceTransportPolicy.Query().CountX(ctx); n != 0 {
		t.Fatalf("policy count after final clear = %d, want 0", n)
	}
	intent := client.SourceRuntimeIntent.Query().Where(entintent.SourceID(101)).OnlyX(ctx)
	if intent.DesiredRevision != 2 {
		t.Fatalf("retained intent desired revision = %d, want 2", intent.DesiredRevision)
	}
}

func TestConcurrentFirstWritesMergeAndAdvanceIntentAtomically(t *testing.T) {
	ctx := context.Background()
	arrived := make(chan *bool, 2)
	release := make(chan struct{})
	defaults := &barrierDefaults{image: sourcetransport.ImageConnectionFresh, arrived: arrived, release: release}
	client := testdb.New(t)
	svc := sourcetransport.NewService(client, defaults, fakeCatalog{})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := svc.Update(ctx, 101, sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(true)})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := svc.Update(ctx, 101, sourcetransport.Patch{ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse)})
		errs <- err
	}()
	var sawExplicitTrue, sawInherited bool
	for range 2 {
		override := <-arrived
		switch {
		case override == nil:
			sawInherited = true
		case *override:
			sawExplicitTrue = true
		default:
			t.Fatalf("unexpected explicit false bypass override during first writes")
		}
	}
	if !sawExplicitTrue || !sawInherited {
		t.Fatalf("pre-write bypass overrides = explicit true:%t inherited:%t, want both patch-local true and independent inherited state", sawExplicitTrue, sawInherited)
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Update: %v", err)
		}
	}

	policy := client.SourceTransportPolicy.Query().Where(entpolicy.SourceID(101)).OnlyX(ctx)
	if policy.ReuseBypassSession == nil || !*policy.ReuseBypassSession || policy.ImageConnectionMode == nil || *policy.ImageConnectionMode != entpolicy.ImageConnectionModeReuse {
		t.Fatalf("merged policy = %+v, want both first-write overrides", policy)
	}
	intent := client.SourceRuntimeIntent.Query().Where(entintent.SourceID(101)).OnlyX(ctx)
	if intent.DesiredRevision != 2 {
		t.Fatalf("desired revision after concurrent updates = %d, want 2", intent.DesiredRevision)
	}
}

type barrierDefaults struct {
	image   sourcetransport.ImageConnectionMode
	arrived chan<- *bool
	release <-chan struct{}
	calls   atomic.Int64
}

func (d *barrierDefaults) ImageConnectionMode(context.Context) sourcetransport.ImageConnectionMode {
	return d.image
}

func (d *barrierDefaults) ResolveBypassSession(_ context.Context, _ int64, override *bool) (bool, sourcetransport.BypassSessionMode, error) {
	if d.calls.Add(1) <= 2 {
		var copied *bool
		if override != nil {
			value := *override
			copied = &value
		}
		d.arrived <- copied
		<-d.release
	}
	if override != nil && *override {
		return true, sourcetransport.BypassSessionReusable, nil
	}
	return false, sourcetransport.BypassSessionDisabled, nil
}

type resultInterleavingDefaults struct {
	image       sourcetransport.ImageConnectionMode
	svc         *sourcetransport.Service
	calls       atomic.Int64
	callbackErr error
}

func (d *resultInterleavingDefaults) ImageConnectionMode(context.Context) sourcetransport.ImageConnectionMode {
	return d.image
}

func (d *resultInterleavingDefaults) ResolveBypassSession(ctx context.Context, _ int64, override *bool) (bool, sourcetransport.BypassSessionMode, error) {
	if d.calls.Add(1) == 2 {
		_, d.callbackErr = d.svc.Update(ctx, 101, sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(true)})
	}
	if override != nil && *override {
		return true, sourcetransport.BypassSessionReusable, nil
	}
	return false, sourcetransport.BypassSessionDisabled, nil
}

func TestUpdateReturnsTransactionCoherentResultAcrossConcurrentFollowUp(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	defaults := &resultInterleavingDefaults{image: sourcetransport.ImageConnectionFresh}
	svc := sourcetransport.NewService(client, defaults, fakeCatalog{})
	defaults.svc = svc

	got, err := svc.Update(ctx, 101, sourcetransport.Patch{ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse)})
	if err != nil {
		t.Fatalf("first Update: %v", err)
	}
	if defaults.callbackErr != nil {
		t.Fatalf("concurrent follow-up Update: %v", defaults.callbackErr)
	}
	if got.Override.ReuseBypassSession != nil || got.Override.ImageConnectionMode == nil || *got.Override.ImageConnectionMode != sourcetransport.ImageConnectionReuse {
		t.Fatalf("first update override = %+v, want its committed image-only policy", got.Override)
	}
	if got.Intent.DesiredRevision != 1 {
		t.Fatalf("first update desired revision = %d, want its committed revision 1", got.Intent.DesiredRevision)
	}
}

func TestAdvanceIntentTxReturnsNextRevisionInCallerTransaction(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()
	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	intent, err := svc.AdvanceIntentTx(ctx, tx, 101)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("AdvanceIntentTx: %v", err)
	}
	if intent.DesiredRevision != 1 {
		_ = tx.Rollback()
		t.Fatalf("desired revision = %d, want 1", intent.DesiredRevision)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestMarkAppliedDoesNotAcknowledgeStaleRevision(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	for range 2 {
		if _, err := svc.Update(ctx, 101, sourcetransport.Patch{ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse)}); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	if err := svc.MarkApplied(ctx, 101, 1); err != nil {
		t.Fatalf("MarkApplied stale revision: %v", err)
	}
	pending, err := svc.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending after stale acknowledgement: %v", err)
	}
	if len(pending) != 1 || pending[0].DesiredRevision != 2 || pending[0].AppliedRevision != 0 {
		t.Fatalf("intent after stale acknowledgement = %+v, want desired 2 / applied 0", pending)
	}
	if err := svc.MarkApplied(ctx, 101, 2); err != nil {
		t.Fatalf("MarkApplied current revision: %v", err)
	}
	pending, err = svc.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending after current acknowledgement: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("Pending after current acknowledgement = %+v, want none", pending)
	}
}

func TestMarkPendingSanitizesAndBoundsApplyError(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	updated, err := svc.Update(ctx, 101, sourcetransport.Patch{ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse)})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := svc.MarkPending(ctx, 101, updated.Intent.DesiredRevision, "  failed\r\n"+strings.Repeat("x", 600)); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}
	pending, err := svc.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("Pending count = %d, want 1", len(pending))
	}
	intent := pending[0]
	if intent.LastApplyAttempt == nil {
		t.Fatal("last apply attempt = nil")
	}
	if len(intent.LastApplyError) > 512 || strings.ContainsAny(intent.LastApplyError, "\r\n") {
		t.Fatalf("sanitized error = %q, want at most 512 bytes without line breaks", intent.LastApplyError)
	}
}

func TestMarkPendingTruncatesAtRuneBoundary(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	updated, err := svc.Update(ctx, 101, sourcetransport.Patch{ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse)})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := svc.MarkPending(ctx, 101, updated.Intent.DesiredRevision, strings.Repeat("a", 511)+"😺"); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}
	pending, err := svc.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || !utf8.ValidString(pending[0].LastApplyError) || len(pending[0].LastApplyError) > 512 {
		t.Fatalf("stored error = %q, want valid UTF-8 at most 512 bytes", pending)
	}
}

func TestMarkPendingDoesNotRecordStaleRevision(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	for range 2 {
		if _, err := svc.Update(ctx, 101, sourcetransport.Patch{ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse)}); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}

	if err := svc.MarkPending(ctx, 101, 1, "stale apply failure"); err != nil {
		t.Fatalf("MarkPending stale revision: %v", err)
	}
	pending, err := svc.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].DesiredRevision != 2 || pending[0].LastApplyAttempt != nil || pending[0].LastApplyError != "" {
		t.Fatalf("intent after stale pending = %+v, want unchanged desired revision 2", pending)
	}
}

func TestUpdateRollsBackPolicyWhenIntentAdvanceFails(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	client.SourceRuntimeIntent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if m, ok := mutation.(*ent.SourceRuntimeIntentMutation); ok && m.Op().Is(ent.OpCreate) {
				return nil, errors.New("injected intent advance failure")
			}
			return next.Mutate(ctx, mutation)
		})
	})
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})

	_, err := svc.Update(ctx, 101, sourcetransport.Patch{ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionReuse)})
	if err == nil || !strings.Contains(err.Error(), "injected intent advance failure") {
		t.Fatalf("Update error = %v, want intent advance failure", err)
	}
	if policies := client.SourceTransportPolicy.Query().CountX(ctx); policies != 0 {
		t.Fatalf("policy rows after failed intent advance = %d, want 0", policies)
	}
	if intents := client.SourceRuntimeIntent.Query().CountX(ctx); intents != 0 {
		t.Fatalf("intent rows after failed intent advance = %d, want 0", intents)
	}
}

func TestAdvanceIntentTxRefreshesUpdatedAtOnConflict(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()
	before := client.SourceRuntimeIntent.Create().
		SetSourceID(101).
		SetUpdatedAt(time.Now().Add(-time.Hour)).
		SaveX(ctx)
	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	if _, err := svc.AdvanceIntentTx(ctx, tx, 101); err != nil {
		_ = tx.Rollback()
		t.Fatalf("AdvanceIntentTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	after := client.SourceRuntimeIntent.GetX(ctx, before.ID)
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated_at after conflict = %v, want after %v", after.UpdatedAt, before.UpdatedAt)
	}
}

func TestUpdateRejectsInvalidImageConnectionModeWithoutPersisting(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{})

	_, err := svc.Update(ctx, 101, sourcetransport.Patch{
		ImageConnectionMode: sourcetransport.Set(sourcetransport.ImageConnectionMode("pooled")),
	})
	if !errors.Is(err, sourcetransport.ErrInvalidPolicy) {
		t.Fatalf("Update error = %v, want ErrInvalidPolicy", err)
	}
	if policies := client.SourceTransportPolicy.Query().CountX(ctx); policies != 0 {
		t.Fatalf("policy rows = %d, want 0", policies)
	}
	if intents := client.SourceRuntimeIntent.Query().CountX(ctx); intents != 0 {
		t.Fatalf("intent rows = %d, want 0", intents)
	}
}

func TestUpdateAbortsBeforePersistingWhenResolverRejectsExplicitReuse(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := sourcetransport.NewService(client, fakeDefaults{
		image: sourcetransport.ImageConnectionFresh,
		resolve: func(_ context.Context, _ int64, override *bool) (bool, sourcetransport.BypassSessionMode, error) {
			if override != nil && *override {
				return false, sourcetransport.BypassSessionDisabled, errors.New("no bypass session selected")
			}
			return false, sourcetransport.BypassSessionDisabled, nil
		},
	}, fakeCatalog{})

	_, err := svc.Update(ctx, 101, sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(true)})
	if err == nil || !strings.Contains(err.Error(), "no bypass session selected") {
		t.Fatalf("Update error = %v, want resolver error", err)
	}
	if policies := client.SourceTransportPolicy.Query().CountX(ctx); policies != 0 {
		t.Fatalf("policy rows = %d, want 0", policies)
	}
	if intents := client.SourceRuntimeIntent.Query().CountX(ctx); intents != 0 {
		t.Fatalf("intent rows = %d, want 0", intents)
	}
}

func TestUpdateRejectsUnknownOrUnavailableCatalogWithoutPersisting(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"unknown", "unavailable"} {
		t.Run(name, func(t *testing.T) {
			client := testdb.New(t)
			svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{err: errUnavailableSource})

			_, err := svc.Update(ctx, 101, sourcetransport.Patch{ReuseBypassSession: sourcetransport.Set(true)})
			if !errors.Is(err, errUnavailableSource) {
				t.Fatalf("Update error = %v, want source catalog error", err)
			}
			if policies := client.SourceTransportPolicy.Query().CountX(ctx); policies != 0 {
				t.Fatalf("policy rows = %d, want 0", policies)
			}
			if intents := client.SourceRuntimeIntent.Query().CountX(ctx); intents != 0 {
				t.Fatalf("intent rows = %d, want 0", intents)
			}
		})
	}
}
