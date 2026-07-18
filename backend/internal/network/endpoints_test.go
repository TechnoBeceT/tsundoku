// Package network_test exercises the per-source network-routing service against
// an ephemeral PostgreSQL instance (testdb). Tests require Docker.
package network_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/network"
)

// socksInput returns a valid SOCKS endpoint input for the tests.
func socksInput(name string) network.EndpointInput {
	return network.EndpointInput{
		Name:         name,
		Kind:         network.KindSocks,
		Enabled:      true,
		Host:         "vpn.local",
		Port:         1080,
		SocksVersion: 5,
		Username:     "user",
		Password:     "secret",
	}
}

// flareInput returns a valid FlareSolverr endpoint input for the tests.
func flareInput(name string) network.EndpointInput {
	return network.EndpointInput{
		Name:    name,
		Kind:    network.KindFlareSolverr,
		Enabled: true,
		URL:     "http://flaresolverr:8191",
		Timeout: 60,
	}
}

// TestCreateEndpoint_SocksRoundTrip proves a valid SOCKS endpoint persists and
// every non-secret field round-trips through List (§16).
func TestCreateEndpoint_SocksRoundTrip(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	created, err := svc.CreateEndpoint(ctx, socksInput("VPN"))
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	assertSocksRoundTrip(t, created)

	list, err := svc.ListEndpoints(ctx)
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("ListEndpoints = %+v, want the one created endpoint", list)
	}
}

// assertSocksRoundTrip checks a created SOCKS endpoint round-tripped every
// non-secret field (split out to keep the test's cyclomatic complexity low).
func assertSocksRoundTrip(t *testing.T, created network.EndpointDTO) {
	t.Helper()
	if created.ID == "" || created.Name != "VPN" || created.Kind != network.KindSocks {
		t.Fatalf("created = %+v, want a socks endpoint named VPN with an id", created)
	}
	if created.Host != "vpn.local" || created.Port != 1080 || created.SocksVersion != 5 || created.Username != "user" {
		t.Fatalf("created socks fields = %+v, want host/port/version/username to round-trip", created)
	}
}

// TestCreateEndpoint_FlareSolverrRoundTrip proves a valid FlareSolverr endpoint
// persists.
func TestCreateEndpoint_FlareSolverrRoundTrip(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	created, err := svc.CreateEndpoint(ctx, flareInput("FS A"))
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	if created.Kind != network.KindFlareSolverr || created.URL != "http://flaresolverr:8191" || created.Timeout != 60 {
		t.Fatalf("created = %+v, want a flaresolverr endpoint", created)
	}
}

// TestCreateEndpoint_KindValidation proves an unknown kind is rejected with
// ErrInvalidEndpoint.
func TestCreateEndpoint_KindValidation(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	in := socksInput("bad")
	in.Kind = "http"
	_, err := svc.CreateEndpoint(context.Background(), in)
	if !errors.Is(err, network.ErrInvalidEndpoint) {
		t.Fatalf("CreateEndpoint bad kind: want ErrInvalidEndpoint, got %v", err)
	}
}

// TestCreateEndpoint_BlankName proves a blank name is rejected regardless of
// kind.
func TestCreateEndpoint_BlankName(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	in := socksInput("")
	_, err := svc.CreateEndpoint(context.Background(), in)
	if !errors.Is(err, network.ErrInvalidEndpoint) {
		t.Fatalf("CreateEndpoint blank name: want ErrInvalidEndpoint, got %v", err)
	}
}

// TestCreateEndpoint_SocksFieldValidation proves each SOCKS field rule fails
// closed.
func TestCreateEndpoint_SocksFieldValidation(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	cases := map[string]func(*network.EndpointInput){
		"blank host":    func(in *network.EndpointInput) { in.Host = "" },
		"port too low":  func(in *network.EndpointInput) { in.Port = 0 },
		"port too high": func(in *network.EndpointInput) { in.Port = 70000 },
		"bad version":   func(in *network.EndpointInput) { in.SocksVersion = 6 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := socksInput("x")
			mutate(&in)
			if _, err := svc.CreateEndpoint(ctx, in); !errors.Is(err, network.ErrInvalidEndpoint) {
				t.Fatalf("%s: want ErrInvalidEndpoint, got %v", name, err)
			}
		})
	}
}

// TestCreateEndpoint_FlareSolverrFieldValidation proves each FlareSolverr field
// rule fails closed.
func TestCreateEndpoint_FlareSolverrFieldValidation(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	cases := map[string]func(*network.EndpointInput){
		"non-http url":     func(in *network.EndpointInput) { in.URL = "ftp://x" },
		"blank url":        func(in *network.EndpointInput) { in.URL = "" },
		"negative timeout": func(in *network.EndpointInput) { in.Timeout = -1 },
		"negative ttl":     func(in *network.EndpointInput) { in.SessionTTL = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := flareInput("x")
			mutate(&in)
			if _, err := svc.CreateEndpoint(ctx, in); !errors.Is(err, network.ErrInvalidEndpoint) {
				t.Fatalf("%s: want ErrInvalidEndpoint, got %v", name, err)
			}
		})
	}
}

// TestUpdateEndpoint_PartialAndPasswordPreserved proves a partial update overlays
// only the given fields, re-validates, and KEEPS the stored password when the
// patch omits it (write-only).
func TestUpdateEndpoint_PartialAndPasswordPreserved(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	created, err := svc.CreateEndpoint(ctx, socksInput("VPN"))
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	id := uuid.MustParse(created.ID)

	newPort := 9050
	updated, err := svc.UpdateEndpoint(ctx, id, network.EndpointPatch{Port: &newPort})
	if err != nil {
		t.Fatalf("UpdateEndpoint: %v", err)
	}
	if updated.Port != 9050 || updated.Host != "vpn.local" {
		t.Fatalf("updated = %+v, want only port changed", updated)
	}

	// The password (omitted from the patch) must survive — read it straight from
	// the row (the DTO never exposes it).
	row, err := client.NetworkEndpoint.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get row: %v", err)
	}
	if row.Password != "secret" {
		t.Fatalf("password = %q, want it preserved (%q) across a patch that omitted it", row.Password, "secret")
	}
}

// TestUpdateEndpoint_PasswordChangedWhenProvided proves a non-nil password patch
// overwrites the stored secret.
func TestUpdateEndpoint_PasswordChangedWhenProvided(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	created, _ := svc.CreateEndpoint(ctx, socksInput("VPN"))
	id := uuid.MustParse(created.ID)

	newPw := "rotated"
	if _, err := svc.UpdateEndpoint(ctx, id, network.EndpointPatch{Password: &newPw}); err != nil {
		t.Fatalf("UpdateEndpoint: %v", err)
	}
	row, _ := client.NetworkEndpoint.Get(ctx, id)
	if row.Password != "rotated" {
		t.Fatalf("password = %q, want %q", row.Password, "rotated")
	}
}

// TestUpdateEndpoint_NotFound proves a missing id yields ErrEndpointNotFound.
func TestUpdateEndpoint_NotFound(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	name := "x"
	_, err := svc.UpdateEndpoint(context.Background(), uuid.New(), network.EndpointPatch{Name: &name})
	if !errors.Is(err, network.ErrEndpointNotFound) {
		t.Fatalf("UpdateEndpoint missing id: want ErrEndpointNotFound, got %v", err)
	}
}

// TestUpdateEndpoint_RevalidatesMergedRow proves the merged row is re-validated —
// patching a socks endpoint's port to an illegal value is rejected.
func TestUpdateEndpoint_RevalidatesMergedRow(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	created, _ := svc.CreateEndpoint(ctx, socksInput("VPN"))
	id := uuid.MustParse(created.ID)

	badPort := 0
	if _, err := svc.UpdateEndpoint(ctx, id, network.EndpointPatch{Port: &badPort}); !errors.Is(err, network.ErrInvalidEndpoint) {
		t.Fatalf("UpdateEndpoint bad port: want ErrInvalidEndpoint, got %v", err)
	}
}

// TestDeleteEndpoint_Unreferenced proves an unbound endpoint deletes cleanly.
func TestDeleteEndpoint_Unreferenced(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	created, _ := svc.CreateEndpoint(ctx, socksInput("VPN"))
	id := uuid.MustParse(created.ID)
	if err := svc.DeleteEndpoint(ctx, id); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	if _, err := svc.UpdateEndpoint(ctx, id, network.EndpointPatch{}); !errors.Is(err, network.ErrEndpointNotFound) {
		t.Fatalf("endpoint still present after delete: %v", err)
	}
}

// TestDeleteEndpoint_BlockedWhenReferenced proves a bound endpoint cannot be
// deleted (ErrEndpointInUse, 409) — the owner-safety guard.
func TestDeleteEndpoint_BlockedWhenReferenced(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	ctx := context.Background()

	socks, _ := svc.CreateEndpoint(ctx, socksInput("VPN"))
	socksID := uuid.MustParse(socks.ID)
	if _, err := svc.SetBinding(ctx, 42, network.BindingInput{
		SocksEndpointID: &socksID,
		FlareMode:       network.FlareModeGlobal,
	}); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}

	err := svc.DeleteEndpoint(ctx, socksID)
	if !errors.Is(err, network.ErrEndpointInUse) {
		t.Fatalf("DeleteEndpoint referenced: want ErrEndpointInUse, got %v", err)
	}
}

// TestDeleteEndpoint_NotFound proves a missing id yields ErrEndpointNotFound.
func TestDeleteEndpoint_NotFound(t *testing.T) {
	client := testdb.New(t)
	svc := network.NewService(client)
	if err := svc.DeleteEndpoint(context.Background(), uuid.New()); !errors.Is(err, network.ErrEndpointNotFound) {
		t.Fatalf("DeleteEndpoint missing id: want ErrEndpointNotFound, got %v", err)
	}
}
