package runtimepolicy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/runtimepolicy"
)

func TestCoordinatorSerializesExplicitOnAgainstGlobalClearInBothOrderings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		onFirst bool
	}{
		{name: "explicit on commits first", onFirst: true},
		{name: "global clear commits first", onFirst: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testdb.New(t)
			ctx := context.Background()
			if _, err := client.Settings.Create().SetKey("flaresolverr.session_name").SetValue("named").Save(ctx); err != nil {
				t.Fatal(err)
			}
			coordinator := runtimepolicy.New(client, "")
			firstEntered := make(chan struct{})
			releaseFirst := make(chan struct{})
			firstDone := make(chan error, 1)
			secondDone := make(chan error, 1)
			on := true
			blank := ""
			onMutation := func(block bool) error {
				return coordinator.Mutate(ctx, runtimepolicy.Proposal{Policies: map[int64]*bool{11: &on}}, func(ctx context.Context) error {
					if block {
						close(firstEntered)
						<-releaseFirst
					}
					_, err := client.SourceTransportPolicy.Create().SetSourceID(11).SetReuseBypassSession(true).Save(ctx)
					return err
				})
			}
			clearMutation := func(block bool) error {
				return coordinator.Mutate(ctx, runtimepolicy.Proposal{GlobalSession: &blank}, func(ctx context.Context) error {
					if block {
						close(firstEntered)
						<-releaseFirst
					}
					_, err := client.Settings.Update().SetValue("").Save(ctx)
					return err
				})
			}
			if tc.onFirst {
				go func() { firstDone <- onMutation(true) }()
			} else {
				go func() { firstDone <- clearMutation(true) }()
			}
			<-firstEntered
			if tc.onFirst {
				go func() { secondDone <- clearMutation(false) }()
			} else {
				go func() { secondDone <- onMutation(false) }()
			}
			select {
			case err := <-secondDone:
				t.Fatalf("second mutation escaped serialization: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			close(releaseFirst)
			if err := <-firstDone; err != nil {
				t.Fatalf("first mutation: %v", err)
			}
			if err := <-secondDone; !errors.Is(err, runtimepolicy.ErrInvalidSelection) {
				t.Fatalf("second error = %v, want ErrInvalidSelection", err)
			}
			if err := coordinator.ValidateCurrent(ctx); err != nil {
				t.Fatalf("committed state violates invariant: %v", err)
			}
		})
	}
}

func TestCoordinatorRejectsSessionClearBeforeCommit(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	if _, err := client.SourceTransportPolicy.Create().SetSourceID(7).SetReuseBypassSession(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	coordinator := runtimepolicy.New(client, "global-session")
	blank := ""
	committed := false
	err := coordinator.Mutate(ctx, runtimepolicy.Proposal{GlobalSession: &blank}, func(context.Context) error {
		committed = true
		return nil
	})
	if !errors.Is(err, runtimepolicy.ErrInvalidSelection) {
		t.Fatalf("Mutate error = %v, want ErrInvalidSelection", err)
	}
	if committed {
		t.Fatal("commit ran for invalid prospective session")
	}
}

func TestCoordinatorRejectsSelectedEndpointSessionClearButAllowsNone(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	endpointID := uuid.New()
	if _, err := client.NetworkEndpoint.Create().SetID(endpointID).SetName("flare").SetKind("flaresolverr").SetEnabled(true).SetURL("http://flare").SetSession("named").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SourceNetworkBinding.Create().SetSourceID(8).SetFlareMode("endpoint").SetFlareEndpointID(endpointID).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SourceTransportPolicy.Create().SetSourceID(8).SetReuseBypassSession(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	coordinator := runtimepolicy.New(client, "")
	invalid := runtimepolicy.Proposal{Endpoints: map[uuid.UUID]*runtimepolicy.Endpoint{endpointID: {
		Kind: "flaresolverr", Enabled: true, Session: "",
	}}}
	if err := coordinator.Mutate(ctx, invalid, func(context.Context) error { return nil }); !errors.Is(err, runtimepolicy.ErrInvalidSelection) {
		t.Fatalf("endpoint clear error = %v, want ErrInvalidSelection", err)
	}
	none := runtimepolicy.Proposal{Bindings: map[int64]*runtimepolicy.Binding{8: {FlareMode: "none"}}}
	if err := coordinator.Mutate(ctx, none, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("none route rejected: %v", err)
	}
}

func TestCoordinatorFailsClosedOnLegacyInvalidExplicitReuse(t *testing.T) {
	client := testdb.New(t)
	if _, err := client.SourceTransportPolicy.Create().SetSourceID(9).SetReuseBypassSession(true).Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := runtimepolicy.New(client, "").ValidateCurrent(context.Background())
	if !errors.Is(err, runtimepolicy.ErrInvalidSelection) {
		t.Fatalf("ValidateCurrent error = %v, want ErrInvalidSelection", err)
	}
}
