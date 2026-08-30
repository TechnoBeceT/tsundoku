package sourcetransport_test

import (
	"context"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

type runtimeStatementCountingDriver struct {
	dialect.Driver
	statements atomic.Int64
}

func (d *runtimeStatementCountingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.statements.Add(1)
	return d.Driver.Query(ctx, query, args, v)
}

func (d *runtimeStatementCountingDriver) Exec(ctx context.Context, query string, args, v any) error {
	d.statements.Add(1)
	return d.Driver.Exec(ctx, query, args, v)
}

func (d *runtimeStatementCountingDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	tx, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &runtimeStatementCountingTx{Tx: tx, driver: d}, nil
}

type runtimeStatementCountingTx struct {
	dialect.Tx
	driver *runtimeStatementCountingDriver
}

func (t *runtimeStatementCountingTx) Query(ctx context.Context, query string, args, v any) error {
	t.driver.statements.Add(1)
	return t.Tx.Query(ctx, query, args, v)
}

func (t *runtimeStatementCountingTx) Exec(ctx context.Context, query string, args, v any) error {
	t.driver.statements.Add(1)
	return t.Tx.Exec(ctx, query, args, v)
}

func newRuntimeStatementCountingClient(t *testing.T) (*ent.Client, *runtimeStatementCountingDriver) {
	t.Helper()
	_, db := testdb.NewWithSQL(t)
	driver := &runtimeStatementCountingDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	return ent.NewClient(ent.Driver(driver)), driver
}

func TestApplyRevisionsStatementCountIsBatchSizeIndependent(t *testing.T) {
	ctx := context.Background()
	measure := func(size int) int64 {
		client, driver := newRuntimeStatementCountingClient(t)
		intents := make([]sourcetransport.Intent, 0, size)
		for sourceID := int64(1); sourceID <= int64(size); sourceID++ {
			desired := sourceID + 2
			seedRuntimeIntent(t, client, sourceID, desired, sourceID)
			intents = append(intents, sourcetransport.Intent{SourceID: sourceID, DesiredRevision: desired})
		}
		svc := sourcetransport.NewService(client, fakeDefaults{image: sourcetransport.ImageConnectionFresh}, fakeCatalog{}).
			WithRuntimeApplier(runtimeApplierFunc(func(context.Context, int64) error { return nil }))
		driver.statements.Store(0)
		if _, err := svc.ApplyRevisions(ctx, intents); err != nil {
			t.Fatalf("ApplyRevisions(%d): %v", size, err)
		}
		return driver.statements.Load()
	}

	one := measure(1)
	many := measure(50)
	if one != many {
		t.Fatalf("statement count grew with batch size: one=%d many=%d", one, many)
	}
	if many != 4 {
		t.Fatalf("ApplyRevisions statements = %d, want bulk check + locked recheck + guarded update + reload", many)
	}
}
