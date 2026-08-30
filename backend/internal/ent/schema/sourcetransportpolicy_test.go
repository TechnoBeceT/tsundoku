package schema_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entpolicy "github.com/technobecet/tsundoku/internal/ent/sourcetransportpolicy"
)

func TestSourceTransportPolicyStoresNullableExplicitFalse(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	created := client.SourceTransportPolicy.Create().
		SetSourceID(101).
		SetReuseBypassSession(false).
		SetImageConnectionMode(entpolicy.ImageConnectionModeReuse).
		SaveX(ctx)

	got := client.SourceTransportPolicy.GetX(ctx, created.ID)
	if got.ReuseBypassSession == nil || *got.ReuseBypassSession {
		t.Fatalf("reuse_bypass_session = %v, want pointer to false", got.ReuseBypassSession)
	}
	if got.ImageConnectionMode == nil || *got.ImageConnectionMode != entpolicy.ImageConnectionModeReuse {
		t.Fatalf("image_connection_mode = %v, want reuse", got.ImageConnectionMode)
	}
}

func TestSourceTransportPolicySourceIDIsUnique(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	client.SourceTransportPolicy.Create().SetSourceID(101).SaveX(ctx)
	_, err := client.SourceTransportPolicy.Create().SetSourceID(101).Save(ctx)
	if err == nil || !ent.IsConstraintError(err) {
		t.Fatalf("duplicate source_id error = %v, want constraint error", err)
	}
}

func TestSourceTransportPolicyRejectsUnknownImageConnectionMode(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	_, err := client.SourceTransportPolicy.Create().
		SetSourceID(101).
		SetImageConnectionMode(entpolicy.ImageConnectionMode("pooled")).
		Save(ctx)
	if err == nil {
		t.Fatal("invalid image_connection_mode error = nil")
	}
}
