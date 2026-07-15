package enginetopo_test

import (
	"context"
	"errors"
	"testing"

	entsourcepreference "github.com/technobecet/tsundoku/internal/ent/sourcepreference"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// TestSeedSourcePreferences_UpsertsEveryTypedValue proves each preference
// variant is mapped to the correct (value, value_type) pair, including an
// EditText preference that looks like a login password — stored VERBATIM
// (no masking/detection), matching the .Sensitive() plaintext-secrets model.
func TestSeedSourcePreferences_UpsertsEveryTypedValue(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedProvider(ctx, t, client, "Solo Leveling", "42", 1001)

	fc := sourceenginefake.New(sourceenginefake.WithPreferences(42, []sourceengine.Preference{
		{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: true},
		{Type: sourceengine.PreferenceSwitchCompat, Key: "useHttps", CurrentValue: false},
		{Type: sourceengine.PreferenceList, Key: "thumbnailQuality", CurrentValue: "high"},
		{Type: sourceengine.PreferenceEditText, Key: "password", CurrentValue: "hunter2-plaintext"},
		{Type: sourceengine.PreferenceMultiSelect, Key: "blockedGroups", CurrentValue: []string{"GroupA", "GroupB"}},
		// Unset current value — must be skipped, not seeded as "".
		{Type: sourceengine.PreferenceEditText, Key: "username", CurrentValue: nil},
	}))

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
		{"nsfw", "true", sourceengine.PreferenceCheckBox},
		{"useHttps", "false", sourceengine.PreferenceSwitchCompat},
		{"thumbnailQuality", "high", sourceengine.PreferenceList},
		{"password", "hunter2-plaintext", sourceengine.PreferenceEditText},
		{"blockedGroups", `["GroupA","GroupB"]`, sourceengine.PreferenceMultiSelect},
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

	fc := sourceenginefake.New(sourceenginefake.WithPreferences(7, []sourceengine.Preference{
		{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: false},
	}))

	if _, err := enginetopo.SeedSourcePreferences(ctx, fc, client); err != nil {
		t.Fatalf("first SeedSourcePreferences: %v", err)
	}
	first := client.SourcePreference.Query().
		Where(entsourcepreference.SourceID(7), entsourcepreference.Key("nsfw")).
		OnlyX(ctx)
	if first.Value != "false" {
		t.Fatalf("first pass value = %q, want false", first.Value)
	}

	// The engine's value changed between passes (e.g. the owner flipped it
	// directly on the engine host) — the second seed must overwrite, not
	// duplicate. SetPreferences mutates the fake's stored preference in place.
	if _, err := fc.SetPreferences(ctx, 7, map[string]any{"nsfw": true}); err != nil {
		t.Fatalf("seed fixture mutation: %v", err)
	}

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

// prefsErrClient wraps the shared sourceengine fake, injecting a per-source
// Preferences failure — the base fake's WithError is blanket (fails every
// call to the named method regardless of source), which can't model "source
// 10 fails, source 11 succeeds" in one client.
type prefsErrClient struct {
	*sourceenginefake.Client
	errBySource map[int64]error
}

func (c *prefsErrClient) Preferences(ctx context.Context, sourceID int64) ([]sourceengine.Preference, error) {
	if err, ok := c.errBySource[sourceID]; ok {
		return nil, err
	}
	return c.Client.Preferences(ctx, sourceID)
}

// TestSeedSourcePreferences_PerSourceFailureSkipsButContinues proves a
// Preferences error on ONE source is logged+skipped (counted in
// SkippedSources) without aborting the seed of every OTHER source.
func TestSeedSourcePreferences_PerSourceFailureSkipsButContinues(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedProvider(ctx, t, client, "Solo Leveling", "10", 3001)
	seedProvider(ctx, t, client, "Omniscient Reader", "11", 3002)

	fc := &prefsErrClient{
		Client: sourceenginefake.New(sourceenginefake.WithPreferences(11, []sourceengine.Preference{
			{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: true},
		})),
		errBySource: map[int64]error{10: errors.New("source offline")},
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
// SeriesProvider row (provider stores a display NAME, not a numeric engine
// source id) is silently skipped — no client call, not counted as a failure.
func TestSeedSourcePreferences_SkipsNonNumericProvider(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedProvider(ctx, t, client, "The Beginning After The End", "Weeb Central", 0)

	fc := sourceenginefake.New()

	result, err := enginetopo.SeedSourcePreferences(ctx, fc, client)
	if err != nil {
		t.Fatalf("SeedSourcePreferences: %v", err)
	}
	if result.Seeded != 0 || result.SkippedSources != 0 {
		t.Fatalf("got Seeded=%d SkippedSources=%d, want 0/0 (disk-origin provider has no engine source)",
			result.Seeded, result.SkippedSources)
	}
	if c := fc.CallCount("Preferences"); c != 0 {
		t.Errorf("Preferences called %d times for a disk-origin provider, want 0", c)
	}
}
