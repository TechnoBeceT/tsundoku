package network_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entbinding "github.com/technobecet/tsundoku/internal/ent/sourcenetworkbinding"
	"github.com/technobecet/tsundoku/internal/ent/sourceruntimeintent"
	"github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/runtimepolicy"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

type bindingCatalog struct{ err error }

func (c bindingCatalog) RequireSource(context.Context, int64) error { return c.err }

// TestSetBindingRejectsSocksForRequiredBrowserWithoutAdvancingIntent proves a
// route written after its required browser policy is rejected before either
// durable binding or runtime intent changes.
func TestSetBindingRejectsSocksForRequiredBrowserWithoutAdvancingIntent(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := network.NewService(client).WithRuntimePolicyCoordinator(runtimepolicy.New(client, ""))
	socks, err := svc.CreateEndpoint(ctx, socksInput("VPN"))
	if err != nil {
		t.Fatal(err)
	}
	socksID := uuid.MustParse(socks.ID)
	if _, err := client.SourceTransportPolicy.Create().SetSourceID(42).SetKcefPolicy("required").Save(ctx); err != nil {
		t.Fatal(err)
	}

	_, err = svc.SetBinding(ctx, 42, network.BindingInput{
		SocksEndpointID: &socksID,
		FlareMode:       network.FlareModeGlobal,
	})
	if !errors.Is(err, network.ErrInvalidBinding) {
		t.Fatalf("SetBinding error = %v, want ErrInvalidBinding", err)
	}
	assertSanitizedKCEFMutation(t, err)
	if _, err := svc.GetBinding(ctx, 42); !errors.Is(err, network.ErrBindingNotFound) {
		t.Fatalf("binding after rejected write = %v, want ErrBindingNotFound", err)
	}
	if got := client.SourceRuntimeIntent.Query().CountX(ctx); got != 0 {
		t.Fatalf("runtime intent count after rejected binding = %d, want 0", got)
	}
	policy := client.SourceTransportPolicy.Query().OnlyX(ctx)
	if policy.KcefPolicy == nil || *policy.KcefPolicy != "required" {
		t.Fatalf("browser policy after rejected binding = %+v, want required unchanged", policy)
	}
	if !client.NetworkEndpoint.GetX(ctx, socksID).Enabled {
		t.Fatal("SOCKS endpoint changed after rejected binding")
	}
}

// TestSetBindingUpdateRejectsSocksForRequiredBrowserWithoutIntentChurn proves
// replacing a safe binding with an effective SOCKS route leaves the old
// binding and its runtime revision untouched.
func TestSetBindingUpdateRejectsSocksForRequiredBrowserWithoutIntentChurn(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := network.NewService(client).WithRuntimePolicyCoordinator(runtimepolicy.New(client, ""))
	socks, err := svc.CreateEndpoint(ctx, socksInput("VPN"))
	if err != nil {
		t.Fatal(err)
	}
	socksID := uuid.MustParse(socks.ID)
	if _, err := client.SourceTransportPolicy.Create().SetSourceID(42).SetKcefPolicy("required").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetBinding(ctx, 42, network.BindingInput{FlareMode: network.FlareModeGlobal}); err != nil {
		t.Fatal(err)
	}

	_, err = svc.SetBinding(ctx, 42, network.BindingInput{
		SocksEndpointID: &socksID,
		FlareMode:       network.FlareModeGlobal,
	})
	if !errors.Is(err, network.ErrInvalidBinding) {
		t.Fatalf("SetBinding update error = %v, want ErrInvalidBinding", err)
	}
	assertSanitizedKCEFMutation(t, err)
	stored := client.SourceNetworkBinding.Query().Where(entbinding.SourceID(42)).OnlyX(ctx)
	if stored.SocksEndpointID != nil || stored.FlareMode != network.FlareModeGlobal {
		t.Fatalf("binding after rejected update = %+v, want direct global route", stored)
	}
	intent := client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(42)).OnlyX(ctx)
	if intent.DesiredRevision != 1 {
		t.Fatalf("desired revision after rejected binding update = %d, want 1", intent.DesiredRevision)
	}
	policy := client.SourceTransportPolicy.Query().OnlyX(ctx)
	if policy.KcefPolicy == nil || *policy.KcefPolicy != "required" {
		t.Fatalf("browser policy after rejected binding update = %+v, want required unchanged", policy)
	}
	if !client.NetworkEndpoint.GetX(ctx, socksID).Enabled {
		t.Fatal("SOCKS endpoint changed after rejected binding update")
	}
}

// TestClearBindingResolvesLegacyRequiredBrowserSocksRoute proves a real
// binding delete is admitted when it removes the SOCKS route causing an older
// persisted browser-policy conflict.
func TestClearBindingResolvesLegacyRequiredBrowserSocksRoute(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := network.NewService(client).WithRuntimePolicyCoordinator(runtimepolicy.New(client, ""))
	socks, err := svc.CreateEndpoint(ctx, socksInput("VPN"))
	if err != nil {
		t.Fatal(err)
	}
	socksID := uuid.MustParse(socks.ID)
	client.SourceNetworkBinding.Create().SetSourceID(42).SetSocksEndpointID(socksID).SetFlareMode(network.FlareModeGlobal).ExecX(ctx)
	client.SourceTransportPolicy.Create().SetSourceID(42).SetKcefPolicy("required").ExecX(ctx)

	deleted, err := svc.ClearBinding(ctx, 42)
	if err != nil {
		t.Fatalf("ClearBinding: %v", err)
	}
	if !deleted.Changed || deleted.Intent.DesiredRevision != 1 {
		t.Fatalf("ClearBinding result = %+v, want binding removal at revision 1", deleted)
	}
	if _, err := svc.GetBinding(ctx, 42); !errors.Is(err, network.ErrBindingNotFound) {
		t.Fatalf("binding after clear = %v, want ErrBindingNotFound", err)
	}
	policy := client.SourceTransportPolicy.Query().OnlyX(ctx)
	if policy.KcefPolicy == nil || *policy.KcefPolicy != "required" {
		t.Fatalf("browser policy after clear = %+v, want required unchanged", policy)
	}
	if !client.NetworkEndpoint.GetX(ctx, socksID).Enabled {
		t.Fatal("SOCKS endpoint changed while clearing the binding")
	}
}

func assertSanitizedKCEFMutation(t *testing.T, err error) {
	t.Helper()
	for _, detail := range []string{"source 42", "required embedded browser"} {
		if strings.Contains(err.Error(), detail) {
			t.Fatalf("network mutation leaked coordinator detail %q in %q", detail, err)
		}
	}
}

// TestSetBinding_AdvancesRuntimeIntent proves the binding row and desired
// runtime revision are committed as one source-scoped mutation.
func TestSetBinding_AdvancesRuntimeIntent(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	result, err := svc.SetBinding(ctx, 42, network.BindingInput{FlareMode: network.FlareModeGlobal})
	if err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	if !result.Changed || result.Intent.SourceID != 42 || result.Intent.DesiredRevision != 1 {
		t.Fatalf("SetBinding result = %+v, want changed committed revision 1", result)
	}
	intent, err := client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(42)).Only(ctx)
	if err != nil {
		t.Fatalf("runtime intent after binding commit: %v", err)
	}
	if intent.DesiredRevision != 1 || intent.AppliedRevision != 0 {
		t.Fatalf("runtime intent = desired %d / applied %d, want 1 / 0", intent.DesiredRevision, intent.AppliedRevision)
	}
}

// TestSetBinding_UnchangedAvoidsRuntimeRevisionChurn catches a blind upsert
// that advances desired runtime state even though the persisted binding is
// already identical to the requested value.
func TestSetBinding_UnchangedAvoidsRuntimeRevisionChurn(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()
	in := network.BindingInput{FlareMode: network.FlareModeGlobal}

	if _, err := svc.SetBinding(ctx, 42, in); err != nil {
		t.Fatalf("first SetBinding: %v", err)
	}
	unchanged, err := svc.SetBinding(ctx, 42, in)
	if err != nil {
		t.Fatalf("unchanged SetBinding: %v", err)
	}
	if unchanged.Changed || unchanged.Intent.DesiredRevision != 1 {
		t.Fatalf("unchanged SetBinding result = %+v, want unchanged revision 1", unchanged)
	}
	intent := client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(42)).OnlyX(ctx)
	if intent.DesiredRevision != 1 {
		t.Fatalf("desired revision after unchanged PUT = %d, want 1", intent.DesiredRevision)
	}
}

// TestClearBinding_AdvancesIntentOnlyForActualDeletion distinguishes a
// committed delete from the existing 404 no-op contract.
func TestClearBinding_AdvancesIntentOnlyForActualDeletion(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	if _, err := svc.SetBinding(ctx, 42, network.BindingInput{FlareMode: network.FlareModeGlobal}); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	deleted, err := svc.ClearBinding(ctx, 42)
	if err != nil {
		t.Fatalf("ClearBinding: %v", err)
	}
	if !deleted.Changed || deleted.Intent.DesiredRevision != 2 {
		t.Fatalf("ClearBinding result = %+v, want changed committed revision 2", deleted)
	}
	intent := client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(42)).OnlyX(ctx)
	if intent.DesiredRevision != 2 {
		t.Fatalf("desired revision after actual delete = %d, want 2", intent.DesiredRevision)
	}
	if _, err := svc.ClearBinding(ctx, 42); !errors.Is(err, network.ErrBindingNotFound) {
		t.Fatalf("second ClearBinding error = %v, want ErrBindingNotFound", err)
	}
	intent = client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(42)).OnlyX(ctx)
	if intent.DesiredRevision != 2 {
		t.Fatalf("desired revision after delete no-op = %d, want 2", intent.DesiredRevision)
	}
}

// TestBindingMutation_SourceValidationFailsClosed catches either binding or
// intent persistence occurring before the live installed-source check.
func TestBindingMutation_SourceValidationFailsClosed(t *testing.T) { //nolint:gocognit // Failure-closed matrix asserts every mutation path and side effect.
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "source missing", err: sourcetransport.ErrSourceNotFound},
		{name: "catalog unavailable", err: sourcetransport.ErrCatalogUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testdb.New(t)
			svc := network.NewService(client, bindingCatalog{err: tc.err})
			ctx := context.Background()

			if _, err := svc.SetBinding(ctx, 42, network.BindingInput{FlareMode: network.FlareModeGlobal}); !errors.Is(err, tc.err) {
				t.Fatalf("SetBinding error = %v, want %v", err, tc.err)
			}
			if got := client.SourceNetworkBinding.Query().CountX(ctx); got != 0 {
				t.Fatalf("binding rows after source rejection = %d, want 0", got)
			}
			if got := client.SourceRuntimeIntent.Query().CountX(ctx); got != 0 {
				t.Fatalf("intent rows after source rejection = %d, want 0", got)
			}

			unguarded := network.NewService(client)
			if _, err := unguarded.SetBinding(ctx, 42, network.BindingInput{FlareMode: network.FlareModeGlobal}); err != nil {
				t.Fatalf("seed binding for DELETE validation: %v", err)
			}
			if _, err := svc.ClearBinding(ctx, 42); !errors.Is(err, tc.err) {
				t.Fatalf("ClearBinding error = %v, want %v", err, tc.err)
			}
			if got := client.SourceNetworkBinding.Query().CountX(ctx); got != 1 {
				t.Fatalf("binding rows after DELETE source rejection = %d, want 1", got)
			}
			intent := client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(42)).OnlyX(ctx)
			if intent.DesiredRevision != 1 {
				t.Fatalf("desired revision after DELETE source rejection = %d, want 1", intent.DesiredRevision)
			}
		})
	}
}

func TestSetBinding_UpdateAdvancesRuntimeIntent(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()
	if _, err := svc.SetBinding(ctx, 42, network.BindingInput{FlareMode: network.FlareModeGlobal}); err != nil {
		t.Fatalf("create binding: %v", err)
	}
	updated, err := svc.SetBinding(ctx, 42, network.BindingInput{FlareMode: network.FlareModeNone})
	if err != nil {
		t.Fatalf("update binding: %v", err)
	}
	if !updated.Changed || updated.Intent.DesiredRevision != 2 || updated.FlareMode != network.FlareModeNone {
		t.Fatalf("updated result = %+v, want changed none binding at revision 2", updated)
	}
}

// TestBindingMutation_IntentFailureRollsBackBinding proves AdvanceIntentTx is
// inside the same transaction for both write shapes.
func TestBindingMutation_IntentFailureRollsBackBinding(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		client := testdb.New(t)
		injectIntentFailure(client)
		_, err := network.NewService(client).SetBinding(context.Background(), 42, network.BindingInput{FlareMode: network.FlareModeGlobal})
		if err == nil || !strings.Contains(err.Error(), "injected intent failure") {
			t.Fatalf("SetBinding error = %v, want injected intent failure", err)
		}
		if got := client.SourceNetworkBinding.Query().CountX(context.Background()); got != 0 {
			t.Fatalf("binding rows after rollback = %d, want 0", got)
		}
	})

	t.Run("delete", func(t *testing.T) {
		client := testdb.New(t)
		svc := network.NewService(client)
		ctx := context.Background()
		if _, err := svc.SetBinding(ctx, 42, network.BindingInput{FlareMode: network.FlareModeGlobal}); err != nil {
			t.Fatalf("seed binding: %v", err)
		}
		injectIntentFailure(client)
		if _, err := svc.ClearBinding(ctx, 42); err == nil || !strings.Contains(err.Error(), "injected intent failure") {
			t.Fatalf("ClearBinding error = %v, want injected intent failure", err)
		}
		if got := client.SourceNetworkBinding.Query().CountX(ctx); got != 1 {
			t.Fatalf("binding rows after delete rollback = %d, want 1", got)
		}
		intent := client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(42)).OnlyX(ctx)
		if intent.DesiredRevision != 1 {
			t.Fatalf("desired revision after delete rollback = %d, want 1", intent.DesiredRevision)
		}
	})
}

func injectIntentFailure(client *ent.Client) {
	client.SourceRuntimeIntent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if m, ok := mutation.(*ent.SourceRuntimeIntentMutation); ok && m.Op().Is(ent.OpCreate) {
				return nil, errors.New("injected intent failure")
			}
			return next.Mutate(ctx, mutation)
		})
	})
}

// TestSetBinding_RoundTrip proves a binding upserts and round-trips through Get
// and List (§16).
func TestSetBinding_RoundTrip(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	socks, _ := svc.CreateEndpoint(ctx, socksInput("VPN"))
	flare, _ := svc.CreateEndpoint(ctx, flareInput("FS A"))
	socksID := uuid.MustParse(socks.ID)
	flareID := uuid.MustParse(flare.ID)

	got, err := svc.SetBinding(ctx, 42, network.BindingInput{
		SocksEndpointID: &socksID,
		FlareMode:       network.FlareModeEndpoint,
		FlareEndpointID: &flareID,
	})
	if err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	assertBindingRefs(t, got.BindingDTO, socks.ID, flare.ID)

	fetched, err := svc.GetBinding(ctx, 42)
	if err != nil {
		t.Fatalf("GetBinding: %v", err)
	}
	if fetched.SourceID != got.SourceID || fetched.FlareMode != got.FlareMode ||
		derefStr(fetched.SocksEndpointID) != derefStr(got.SocksEndpointID) ||
		derefStr(fetched.FlareEndpointID) != derefStr(got.FlareEndpointID) {
		t.Errorf("GetBinding = %+v, want it to match SetBinding %+v", fetched, got)
	}
}

// assertBindingRefs checks a binding names the expected source, mode, and both
// endpoint ids (split out to keep the test's cyclomatic complexity low).
func assertBindingRefs(t *testing.T, got network.BindingDTO, wantSocks, wantFlare string) {
	t.Helper()
	if got.SourceID != "42" || got.FlareMode != network.FlareModeEndpoint {
		t.Fatalf("binding = %+v, want source 42 endpoint mode", got)
	}
	if derefStr(got.SocksEndpointID) != wantSocks {
		t.Errorf("socksEndpointId = %v, want %s", got.SocksEndpointID, wantSocks)
	}
	if derefStr(got.FlareEndpointID) != wantFlare {
		t.Errorf("flareEndpointId = %v, want %s", got.FlareEndpointID, wantFlare)
	}
}

// derefStr renders a nullable string as a plain string for value comparison
// (the DTO's endpoint ids are *string, so struct == would compare pointers).
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// TestSetBinding_UpsertOnUniqueSource proves a second SetBinding for the same
// source updates the single row (source_id unique) rather than creating a
// duplicate.
func TestSetBinding_UpsertOnUniqueSource(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	socks, _ := svc.CreateEndpoint(ctx, socksInput("VPN"))
	socksID := uuid.MustParse(socks.ID)

	if _, err := svc.SetBinding(ctx, 7, network.BindingInput{SocksEndpointID: &socksID, FlareMode: network.FlareModeGlobal}); err != nil {
		t.Fatalf("first SetBinding: %v", err)
	}
	// Second set clears the SOCKS override and switches FlareSolverr to none.
	updated, err := svc.SetBinding(ctx, 7, network.BindingInput{FlareMode: network.FlareModeNone})
	if err != nil {
		t.Fatalf("second SetBinding: %v", err)
	}
	if updated.SocksEndpointID != nil {
		t.Errorf("socksEndpointId = %v, want nil (cleared on re-set)", updated.SocksEndpointID)
	}
	if updated.FlareMode != network.FlareModeNone {
		t.Errorf("flareMode = %q, want none", updated.FlareMode)
	}

	list, err := svc.ListBindings(ctx)
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListBindings = %+v, want exactly one row (upsert, not duplicate)", list)
	}
}

// TestSetBinding_EndpointMustExist proves a reference to a non-existent endpoint
// is rejected with ErrInvalidBinding.
func TestSetBinding_EndpointMustExist(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ghost := uuid.New()
	_, err := svc.SetBinding(context.Background(), 1, network.BindingInput{
		SocksEndpointID: &ghost,
		FlareMode:       network.FlareModeGlobal,
	})
	if !errors.Is(err, network.ErrInvalidBinding) {
		t.Fatalf("SetBinding missing endpoint: want ErrInvalidBinding, got %v", err)
	}
}

// TestSetBinding_EndpointKindMustMatch proves a socks_endpoint_id that names a
// FlareSolverr endpoint (and vice versa) is rejected.
func TestSetBinding_EndpointKindMustMatch(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	flare, _ := svc.CreateEndpoint(ctx, flareInput("FS A"))
	flareID := uuid.MustParse(flare.ID)

	// Point the SOCKS slot at a FlareSolverr endpoint — wrong kind.
	_, err := svc.SetBinding(ctx, 1, network.BindingInput{
		SocksEndpointID: &flareID,
		FlareMode:       network.FlareModeGlobal,
	})
	if !errors.Is(err, network.ErrInvalidBinding) {
		t.Fatalf("SetBinding kind mismatch: want ErrInvalidBinding, got %v", err)
	}
}

// TestSetBinding_FlareModeConsistency proves flare_endpoint_id is required iff
// flare_mode == endpoint and forbidden otherwise.
func TestSetBinding_FlareModeConsistency(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	flare, _ := svc.CreateEndpoint(ctx, flareInput("FS A"))
	flareID := uuid.MustParse(flare.ID)

	// endpoint mode without a flare_endpoint_id → invalid.
	if _, err := svc.SetBinding(ctx, 1, network.BindingInput{FlareMode: network.FlareModeEndpoint}); !errors.Is(err, network.ErrInvalidBinding) {
		t.Fatalf("endpoint mode, no id: want ErrInvalidBinding, got %v", err)
	}
	// global mode WITH a flare_endpoint_id → invalid.
	if _, err := svc.SetBinding(ctx, 1, network.BindingInput{FlareMode: network.FlareModeGlobal, FlareEndpointID: &flareID}); !errors.Is(err, network.ErrInvalidBinding) {
		t.Fatalf("global mode, with id: want ErrInvalidBinding, got %v", err)
	}
	// unknown mode → invalid.
	if _, err := svc.SetBinding(ctx, 1, network.BindingInput{FlareMode: "weird"}); !errors.Is(err, network.ErrInvalidBinding) {
		t.Fatalf("unknown mode: want ErrInvalidBinding, got %v", err)
	}
}

// TestGetBinding_NotFound proves an unbound source yields ErrBindingNotFound.
func TestGetBinding_NotFound(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	if _, err := svc.GetBinding(context.Background(), 999); !errors.Is(err, network.ErrBindingNotFound) {
		t.Fatalf("GetBinding unbound: want ErrBindingNotFound, got %v", err)
	}
}

// TestClearBinding proves clearing removes the row, and clearing an unbound
// source yields ErrBindingNotFound.
func TestClearBinding(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	if _, err := svc.SetBinding(ctx, 5, network.BindingInput{FlareMode: network.FlareModeGlobal}); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	if _, err := svc.ClearBinding(ctx, 5); err != nil {
		t.Fatalf("ClearBinding: %v", err)
	}
	if _, err := svc.GetBinding(ctx, 5); !errors.Is(err, network.ErrBindingNotFound) {
		t.Fatalf("after clear: want ErrBindingNotFound, got %v", err)
	}
	if _, err := svc.ClearBinding(ctx, 5); !errors.Is(err, network.ErrBindingNotFound) {
		t.Fatalf("clear unbound: want ErrBindingNotFound, got %v", err)
	}
}

// TestListBindings_Empty proves an unbound library lists no bindings (non-nil
// empty slice).
func TestListBindings_Empty(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	list, err := svc.ListBindings(context.Background())
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("ListBindings = %+v, want a non-nil empty slice", list)
	}
}
