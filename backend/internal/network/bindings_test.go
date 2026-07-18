package network_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/network"
)

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
	assertBindingRefs(t, got, socks.ID, flare.ID)

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
	if err := svc.ClearBinding(ctx, 5); err != nil {
		t.Fatalf("ClearBinding: %v", err)
	}
	if _, err := svc.GetBinding(ctx, 5); !errors.Is(err, network.ErrBindingNotFound) {
		t.Fatalf("after clear: want ErrBindingNotFound, got %v", err)
	}
	if err := svc.ClearBinding(ctx, 5); !errors.Is(err, network.ErrBindingNotFound) {
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
