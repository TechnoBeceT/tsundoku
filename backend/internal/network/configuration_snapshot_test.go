package network_test

import (
	"context"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/network"
)

func TestConfigurationSnapshotReadsEachBackingSetOnce(t *testing.T) { //nolint:cyclop // Snapshot oracle checks each backing store independently.
	ctx := context.Background()
	client := testdb.New(t)
	endpoint := client.NetworkEndpoint.Create().
		SetName("VPN").SetKind(network.KindSocks).SetEnabled(true).
		SetHost("proxy").SetPort(1080).SetSocksVersion(5).SaveX(ctx)
	client.SourceNetworkBinding.Create().SetSourceID(42).SetSocksEndpointID(endpoint.ID).SetFlareMode(network.FlareModeGlobal).ExecX(ctx)

	queries := 0
	client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			queries++
			return next.Query(ctx, query)
		})
	}))

	got, err := network.NewService(client).ConfigurationSnapshot(ctx)
	if err != nil {
		t.Fatalf("ConfigurationSnapshot: %v", err)
	}
	if queries != 2 {
		t.Fatalf("backing queries = %d, want exactly 2 (one endpoints + one bindings)", queries)
	}
	if len(got.Resolved) != 1 || got.Resolved[0].Socks == nil || got.Resolved[0].Socks.ID != endpoint.ID.String() {
		t.Fatalf("resolved = %+v", got.Resolved)
	}
	if len(got.Stored) != 1 || got.Stored[0].SourceID != 42 || got.Stored[0].SocksEndpointID == nil || *got.Stored[0].SocksEndpointID != endpoint.ID.String() {
		t.Fatalf("stored = %+v", got.Stored)
	}
	if got.EndpointNames[endpoint.ID.String()] != "VPN" {
		t.Fatalf("endpoint names = %+v", got.EndpointNames)
	}
}

func TestConfigurationSnapshotDoesNotMixCommitBetweenBackingReads(t *testing.T) { //nolint:cyclop // Atomicity timeline requires explicit synchronization and timeout branches.
	ctx := context.Background()
	client := testdb.New(t)
	endpoint := client.NetworkEndpoint.Create().
		SetName("old-name").SetKind(network.KindSocks).SetEnabled(true).
		SetHost("old-host").SetPort(1080).SetSocksVersion(5).SaveX(ctx)
	binding := client.SourceNetworkBinding.Create().SetSourceID(42).SetSocksEndpointID(endpoint.ID).SetFlareMode(network.FlareModeGlobal).SaveX(ctx)

	endpointRead := make(chan struct{})
	continueRead := make(chan struct{})
	var once sync.Once
	client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if _, ok := query.(*ent.NetworkEndpointQuery); ok && err == nil {
				once.Do(func() {
					close(endpointRead)
					<-continueRead
				})
			}
			return value, err
		})
	}))

	type result struct {
		snapshot network.ConfigurationSnapshot
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		snapshot, err := network.NewService(client).ConfigurationSnapshot(ctx)
		resultCh <- result{snapshot: snapshot, err: err}
	}()
	<-endpointRead

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	if err := tx.NetworkEndpoint.UpdateOneID(endpoint.ID).SetName("new-name").SetHost("new-host").Exec(ctx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("update endpoint: %v", err)
	}
	if err := tx.SourceNetworkBinding.UpdateOneID(binding.ID).ClearSocksEndpointID().SetFlareMode(network.FlareModeNone).Exec(ctx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("update binding: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit writer: %v", err)
	}
	close(continueRead)

	got := <-resultCh
	if got.err != nil {
		t.Fatalf("ConfigurationSnapshot: %v", got.err)
	}
	if got.snapshot.EndpointNames[endpoint.ID.String()] != "old-name" || len(got.snapshot.Stored) != 1 || got.snapshot.Stored[0].SocksEndpointID == nil || got.snapshot.Stored[0].FlareMode != network.FlareModeGlobal {
		t.Fatalf("mixed snapshot = %+v", got.snapshot)
	}

	after, err := network.NewService(client).ConfigurationSnapshot(ctx)
	if err != nil {
		t.Fatalf("ConfigurationSnapshot after commit: %v", err)
	}
	if after.EndpointNames[endpoint.ID.String()] != "new-name" || len(after.Stored) != 1 || after.Stored[0].SocksEndpointID != nil || after.Stored[0].FlareMode != network.FlareModeNone {
		t.Fatalf("after snapshot = %+v", after)
	}
}
