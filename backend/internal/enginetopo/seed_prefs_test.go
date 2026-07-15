package enginetopo_test

import (
	"context"
	"errors"
	"testing"

	entsourcepreference "github.com/technobecet/tsundoku/internal/ent/sourcepreference"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// boolPtr / stringPtr are small pointer-literal helpers for building
// suwayomi.SourcePreference fixtures below.
func boolPtr(v bool) *bool       { return &v }
func stringPtr(v string) *string { return &v }

// TestSeedSourcePreferences_UpsertsEveryTypedValue proves each preference
// variant is mapped to the correct (value, value_type) pair, including an
// EditText preference that looks like a login password — stored VERBATIM
// (no masking/detection), matching the .Sensitive() plaintext-secrets model.
func TestSeedSourcePreferences_UpsertsEveryTypedValue(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedProvider(ctx, t, client, "Solo Leveling", "42", 1001)

	fc := &fakeClient{
		prefsBySource: map[string][]suwayomi.SourcePreference{
			"42": {
				{Type: suwayomi.PreferenceCheckBox, Position: 0, Key: "nsfw", CurrentBool: boolPtr(true)},
				{Type: suwayomi.PreferenceSwitch, Position: 1, Key: "useHttps", CurrentBool: boolPtr(false)},
				{Type: suwayomi.PreferenceList, Position: 2, Key: "thumbnailQuality", CurrentString: stringPtr("high")},
				{Type: suwayomi.PreferenceEditText, Position: 3, Key: "password", CurrentString: stringPtr("hunter2-plaintext")},
				{Type: suwayomi.PreferenceMultiSelect, Position: 4, Key: "blockedGroups", CurrentStringList: []string{"GroupA", "GroupB"}},
				// Unset current value — must be skipped, not seeded as "".
				{Type: suwayomi.PreferenceEditText, Position: 5, Key: "username", CurrentString: nil},
			},
		},
	}

	result, err := enginetopo.SeedSourcePreferences(ctx, fc, client)
	if err != nil {
		t.Fatalf("SeedSourcePreferences: %v", err)
	}
	if result.Seeded != 5 {
		t.Fatalf("Seeded = %d, want 5 (the unset 'username' pref must be skipped)", result.Seeded)
	}
	if result.SkippedSources != 0 {
		t.Fatalf("SkippedSources = %d, want 0", result.SkippedSources)
	}

	rows := client.SourcePreference.Query().AllX(ctx)
	if len(rows) != 5 {
		t.Fatalf("got %d SourcePreference rows, want 5", len(rows))
	}

	wantRows := []struct {
		key       string
		value     string
		valueType string
	}{
		{"nsfw", "true", string(suwayomi.PreferenceCheckBox)},
		{"useHttps", "false", string(suwayomi.PreferenceSwitch)},
		{"thumbnailQuality", "high", string(suwayomi.PreferenceList)},
		{"password", "hunter2-plaintext", string(suwayomi.PreferenceEditText)},
		{"blockedGroups", `["GroupA","GroupB"]`, string(suwayomi.PreferenceMultiSelect)},
	}
	assertSeededRows(t, rows, 42, wantRows)
}

// assertSeededRows asserts every row in got carries sourceID, then matches
// each (key, value, value_type) in want against the corresponding row. It is
// split out of the test bodies above purely to keep their cyclomatic
// complexity within the project's cyclop gate.
func assertSeededRows(t *testing.T, got []*ent.SourcePreference, sourceID int64, want []struct {
	key       string
	value     string
	valueType string
}) {
	t.Helper()
	byKey := make(map[string]*ent.SourcePreference, len(got))
	for _, r := range got {
		byKey[r.Key] = r
		if r.SourceID != sourceID {
			t.Errorf("row %s: source_id = %d, want %d", r.Key, r.SourceID, sourceID)
		}
	}
	for _, tc := range want {
		row, ok := byKey[tc.key]
		if !ok {
			t.Errorf("missing row for key %q", tc.key)
			continue
		}
		if row.Value != tc.value {
			t.Errorf("key %q value = %q, want %q", tc.key, row.Value, tc.value)
		}
		if row.ValueType != tc.valueType {
			t.Errorf("key %q value_type = %q, want %q", tc.key, row.ValueType, tc.valueType)
		}
	}
}

// TestSeedSourcePreferences_IdempotentSecondRun proves a second pass over an
// unchanged engine updates the SAME row in place (no duplicate rows for the
// same (source_id, key)), reflecting the engine's latest value.
func TestSeedSourcePreferences_IdempotentSecondRun(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedProvider(ctx, t, client, "Omniscient Reader", "7", 2001)

	fc := &fakeClient{
		prefsBySource: map[string][]suwayomi.SourcePreference{
			"7": {
				{Type: suwayomi.PreferenceCheckBox, Position: 0, Key: "nsfw", CurrentBool: boolPtr(false)},
			},
		},
	}

	if _, err := enginetopo.SeedSourcePreferences(ctx, fc, client); err != nil {
		t.Fatalf("first SeedSourcePreferences: %v", err)
	}
	first := client.SourcePreference.Query().
		Where(entsourcepreference.SourceID(7), entsourcepreference.Key("nsfw")).
		OnlyX(ctx)
	if first.Value != "false" {
		t.Fatalf("first pass value = %q, want false", first.Value)
	}

	// The engine's value changed between passes (e.g. the owner flipped it in
	// the Suwayomi UI directly) — the second seed must overwrite, not duplicate.
	fc.prefsBySource["7"][0].CurrentBool = boolPtr(true)

	result, err := enginetopo.SeedSourcePreferences(ctx, fc, client)
	if err != nil {
		t.Fatalf("second SeedSourcePreferences: %v", err)
	}
	if result.Seeded != 1 {
		t.Fatalf("second pass Seeded = %d, want 1", result.Seeded)
	}

	rows := client.SourcePreference.Query().
		Where(entsourcepreference.SourceID(7), entsourcepreference.Key("nsfw")).
		AllX(ctx)
	if len(rows) != 1 {
		t.Fatalf("got %d rows for (7,nsfw), want exactly 1 (upsert, not duplicate)", len(rows))
	}
	if rows[0].ID != first.ID {
		t.Error("second pass created a NEW row instead of updating the existing one")
	}
	if rows[0].Value != "true" {
		t.Errorf("row value after second pass = %q, want true", rows[0].Value)
	}
}

// TestSeedSourcePreferences_PerSourceFailureSkipsButContinues proves a
// SourcePreferences error on ONE source is logged+skipped (counted in
// SkippedSources) without aborting the seed of every OTHER source.
func TestSeedSourcePreferences_PerSourceFailureSkipsButContinues(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedProvider(ctx, t, client, "Solo Leveling", "10", 3001)
	seedProvider(ctx, t, client, "Omniscient Reader", "11", 3002)

	fc := &fakeClient{
		prefsBySource: map[string][]suwayomi.SourcePreference{
			"11": {
				{Type: suwayomi.PreferenceCheckBox, Position: 0, Key: "nsfw", CurrentBool: boolPtr(true)},
			},
		},
		prefsErrBySource: map[string]error{
			"10": errors.New("source offline"),
		},
	}

	result, err := enginetopo.SeedSourcePreferences(ctx, fc, client)
	if err != nil {
		t.Fatalf("SeedSourcePreferences: %v", err)
	}
	if result.SkippedSources != 1 {
		t.Errorf("SkippedSources = %d, want 1", result.SkippedSources)
	}
	if result.Seeded != 1 {
		t.Errorf("Seeded = %d, want 1 (source 11's preference)", result.Seeded)
	}

	got := client.SourcePreference.Query().AllX(ctx)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 (only source 11 seeded)", len(got))
	}
	if got[0].SourceID != 11 {
		t.Errorf("seeded row source_id = %d, want 11", got[0].SourceID)
	}
}

// TestSeedSourcePreferences_SkipsNonNumericProvider proves a disk-origin
// SeriesProvider row (provider stores a display NAME, not a numeric Suwayomi
// source id) is silently skipped — no client call, not counted as a failure.
func TestSeedSourcePreferences_SkipsNonNumericProvider(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedProvider(ctx, t, client, "The Beginning After The End", "Weeb Central", 0)

	fc := &fakeClient{}

	result, err := enginetopo.SeedSourcePreferences(ctx, fc, client)
	if err != nil {
		t.Fatalf("SeedSourcePreferences: %v", err)
	}
	if result.Seeded != 0 || result.SkippedSources != 0 {
		t.Fatalf("got Seeded=%d SkippedSources=%d, want 0/0 (disk-origin provider has no engine source)",
			result.Seeded, result.SkippedSources)
	}
	if c := fc.prefsCallCount("Weeb Central"); c != 0 {
		t.Errorf("SourcePreferences called %d times for a disk-origin provider, want 0", c)
	}
}
