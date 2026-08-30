package schema_test

import (
	"context"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent/sourceruntimeintent"
)

func TestSourceRuntimeIntentRevisionsAreInt64AndDefaultToZero(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	created := client.SourceRuntimeIntent.Create().SetSourceID(101).SaveX(ctx)
	if created.DesiredRevision != 0 || created.AppliedRevision != 0 {
		t.Fatalf("initial revisions = desired:%d applied:%d, want 0:0", created.DesiredRevision, created.AppliedRevision)
	}

	got := client.SourceRuntimeIntent.UpdateOneID(created.ID).
		AddDesiredRevision(1).
		AddDesiredRevision(1).
		SaveX(ctx)
	if got.DesiredRevision != 2 {
		t.Fatalf("desired_revision = %d, want monotonic increment to 2", got.DesiredRevision)
	}
}

func TestSourceRuntimeIntentRejectsOversizedApplyError(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	_, err := client.SourceRuntimeIntent.Create().
		SetSourceID(101).
		SetLastApplyError(strings.Repeat("x", 513)).
		Save(ctx)
	if err == nil {
		t.Fatal("oversized last_apply_error error = nil")
	}
}

func TestSourceRuntimeIntentSurvivesPolicyDeletion(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	policy := client.SourceTransportPolicy.Create().SetSourceID(101).SaveX(ctx)
	client.SourceRuntimeIntent.Create().
		SetSourceID(101).
		SetDesiredRevision(3).
		SaveX(ctx)
	client.SourceTransportPolicy.DeleteOne(policy).ExecX(ctx)

	intent := client.SourceRuntimeIntent.Query().Where(sourceruntimeintent.SourceID(101)).OnlyX(ctx)
	if intent.DesiredRevision != 3 {
		t.Fatalf("desired_revision after policy deletion = %d, want 3", intent.DesiredRevision)
	}
}
