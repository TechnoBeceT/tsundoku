package settings_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	settingssvc "github.com/technobecet/tsundoku/internal/settings"
)

func TestImpersonateSourceMembershipTxSupportsSignedIDsAndCanonicalOrdering(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := settingssvc.NewService(client, settingssvc.Defaults{})

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	ids, err := svc.UpdateImpersonateSourceTx(ctx, tx, 1998416842837112832, true)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("enable large source: %v", err)
	}
	ids, err = svc.UpdateImpersonateSourceTx(ctx, tx, -42, true)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("enable signed source: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	want := []int64{-42, 1998416842837112832}
	assertInt64s(t, ids, want)
	assertInt64s(t, svc.ImpersonateSources(ctx), want)
	row := findSetting(t, svc.List(ctx), settingssvc.KeyImpersonateSources)
	if row.Value != "-42,1998416842837112832" {
		t.Fatalf("stored membership = %q, want canonical signed ordering", row.Value)
	}
}

func TestImpersonateSourceMembershipTxDisablesExactlyOneSource(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := settingssvc.NewService(client, settingssvc.Defaults{})
	if err := svc.Set(ctx, settingssvc.KeyImpersonateSources, "-42,7,99"); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	ids, err := svc.UpdateImpersonateSourceTx(ctx, tx, 7, false)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("disable source: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	want := []int64{-42, 99}
	assertInt64s(t, ids, want)
	assertInt64s(t, svc.ImpersonateSources(ctx), want)
}

func assertInt64s(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}
