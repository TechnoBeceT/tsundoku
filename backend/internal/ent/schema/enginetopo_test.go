package schema_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
)

// TestEngineTopoMigratesClean verifies the three additive engine-topology
// entities (HarvestedRepo, HarvestedExtension, SourcePreference) land on a fresh
// DB: testdb.New already migrates the full schema, so a successful acquisition
// plus an empty-table count on each proves the new tables exist and migrated
// clean (zero-data migration).
func TestEngineTopoMigratesClean(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	if n := client.HarvestedRepo.Query().CountX(ctx); n != 0 {
		t.Fatalf("expected empty harvested_repos table, got %d rows", n)
	}
	if n := client.HarvestedExtension.Query().CountX(ctx); n != 0 {
		t.Fatalf("expected empty harvested_extensions table, got %d rows", n)
	}
	if n := client.SourcePreference.Query().CountX(ctx); n != 0 {
		t.Fatalf("expected empty source_preferences table, got %d rows", n)
	}
}

// TestHarvestedRepoRoundTrips verifies a HarvestedRepo create+read round-trip.
func TestHarvestedRepoRoundTrips(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	created := client.HarvestedRepo.Create().
		SetURL("https://raw.githubusercontent.com/example/repo/main/index.min.json").
		SaveX(ctx)

	got := client.HarvestedRepo.GetX(ctx, created.ID)
	if got.URL != "https://raw.githubusercontent.com/example/repo/main/index.min.json" {
		t.Fatalf("url did not round-trip, got %q", got.URL)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("expected created_at/updated_at to be defaulted, got %v / %v", got.CreatedAt, got.UpdatedAt)
	}
}

// TestHarvestedExtensionRoundTrips verifies a HarvestedExtension create+read
// round-trip, including the source_ids JSON column.
func TestHarvestedExtensionRoundTrips(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	created := client.HarvestedExtension.Create().
		SetPkgName("eu.kanade.tachiyomi.extension.en.example").
		SetRepoURL("https://raw.githubusercontent.com/example/repo/main/index.min.json").
		SetVersionCode(42).
		SetVersionName("1.4.2").
		SetSourceIds([]int64{1234567890, 9876543210}).
		SetApkSha256("deadbeef").
		SetApkCached(true).
		SaveX(ctx)

	got := client.HarvestedExtension.GetX(ctx, created.ID)
	if got.PkgName != "eu.kanade.tachiyomi.extension.en.example" {
		t.Fatalf("pkg_name did not round-trip, got %q", got.PkgName)
	}
	if got.VersionCode != 42 || got.VersionName != "1.4.2" {
		t.Fatalf("version did not round-trip, got %d / %q", got.VersionCode, got.VersionName)
	}
	if got.ApkSha256 != "deadbeef" || !got.ApkCached {
		t.Fatalf("apk fields did not round-trip, got %q / %v", got.ApkSha256, got.ApkCached)
	}
	if len(got.SourceIds) != 2 || got.SourceIds[0] != 1234567890 || got.SourceIds[1] != 9876543210 {
		t.Fatalf("source_ids JSON did not round-trip, got %v", got.SourceIds)
	}
}

// TestSourcePreferenceRoundTrips verifies a SourcePreference create+read
// round-trip. The value is .Sensitive() (never logged/serialized) but the plain
// column still stores and returns it to Go code.
func TestSourcePreferenceRoundTrips(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	created := client.SourcePreference.Create().
		SetSourceID(1234567890).
		SetKey("username").
		SetValue("owner@example.com").
		SetValueType("string").
		SaveX(ctx)

	got := client.SourcePreference.GetX(ctx, created.ID)
	if got.SourceID != 1234567890 {
		t.Fatalf("source_id did not round-trip, got %d", got.SourceID)
	}
	if got.Key != "username" || got.Value != "owner@example.com" || got.ValueType != "string" {
		t.Fatalf("key/value/value_type did not round-trip, got %q / %q / %q", got.Key, got.Value, got.ValueType)
	}
}

// TestSourcePreferenceKeyIsUniquePerSource verifies the (source_id, key) unique
// index fires: a source may carry many preferences but never two rows for the
// same key, while the same key under a DIFFERENT source is allowed.
func TestSourcePreferenceKeyIsUniquePerSource(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	client.SourcePreference.Create().
		SetSourceID(1234567890).
		SetKey("username").
		SetValue("first").
		SaveX(ctx)

	// Same (source_id, key) must violate the unique index.
	_, err := client.SourcePreference.Create().
		SetSourceID(1234567890).
		SetKey("username").
		SetValue("second").
		Save(ctx)
	if err == nil || !ent.IsConstraintError(err) {
		t.Fatalf("expected unique constraint violation on duplicate (source_id, key), got %v", err)
	}

	// The same key under a DIFFERENT source must succeed.
	client.SourcePreference.Create().
		SetSourceID(9876543210).
		SetKey("username").
		SetValue("other-source").
		SaveX(ctx)
}
