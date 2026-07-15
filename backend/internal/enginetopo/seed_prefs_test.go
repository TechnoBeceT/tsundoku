package enginetopo_test

import (
	"context"
	"errors"
	"testing"

	entsourcepreference "github.com/technobecet/tsundoku/internal/ent/sourcepreference"
	entsourceseedstate "github.com/technobecet/tsundoku/internal/ent/sourceseedstate"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
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

// seedNamedProvider creates a Series + one SeriesProvider carrying a display
// provider_name (the human-readable name SeedSourcePreferences stores on the
// SourceSeedState row), keyed by the numeric provider source id.
func seedNamedProvider(ctx context.Context, t *testing.T, client *ent.Client, title, provider, name string) {
	t.Helper()
	s := client.Series.Create().
		SetTitle(title).
		SetSlug(disk.Slugify(title)).
		SaveX(ctx)
	client.SeriesProvider.Create().
		SetSeries(s).
		SetProvider(provider).
		SetProviderName(name).
		SetSuwayomiID(1).
		SaveX(ctx)
}

// seedState fetches the single SourceSeedState row for a source id (fails the
// test if absent).
func seedState(ctx context.Context, t *testing.T, client *ent.Client, sourceID int64) *ent.SourceSeedState {
	t.Helper()
	return client.SourceSeedState.Query().
		Where(entsourceseedstate.SourceID(sourceID)).
		OnlyX(ctx)
}

// TestSeedSourcePreferences_RecordsPerSourceReadOutcome proves the SourceSeedState
// bookkeeping across the three distinct read outcomes in ONE pass:
//   - (a) a source with a set preference → prefs_read_ok=true, pref captured,
//     prefs_read_at non-nil, source_name stored;
//   - (b) a source whose read errors → prefs_read_ok=false, last_error set,
//     SkippedSources bumped, prefs_read_at nil (the read never succeeded);
//   - (c) a reached source with zero/all-default prefs → prefs_read_ok=true,
//     zero SourcePreference rows, but STILL a SourceSeedState row (the benign-empty
//     case the old missing-count could not distinguish from a real failure).
func TestSeedSourcePreferences_RecordsPerSourceReadOutcome(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedNamedProvider(ctx, t, client, "Solo Leveling", "42", "Comix")                      // (a)
	seedNamedProvider(ctx, t, client, "Omniscient Reader", "10", "Asura Scans")            // (b)
	seedNamedProvider(ctx, t, client, "The Beginning After The End", "77", "Weeb Central") // (c)

	fc := &fakeClient{
		prefsBySource: map[string][]suwayomi.SourcePreference{
			"42": {{Type: suwayomi.PreferenceCheckBox, Position: 0, Key: "nsfw", CurrentBool: boolPtr(true)}},
			// Source 77 answers with an UNSET preference → zero SourcePreference rows,
			// but the read itself SUCCEEDED (ok=true).
			"77": {{Type: suwayomi.PreferenceEditText, Position: 0, Key: "username", CurrentString: nil}},
		},
		prefsErrBySource: map[string]error{
			"10": errors.New("source offline"),
		},
	}

	result, err := enginetopo.SeedSourcePreferences(ctx, fc, client)
	if err != nil {
		t.Fatalf("SeedSourcePreferences: %v", err)
	}
	if result.Seeded != 1 {
		t.Errorf("Seeded = %d, want 1 (only source 42's nsfw)", result.Seeded)
	}
	if result.SkippedSources != 1 {
		t.Errorf("SkippedSources = %d, want 1 (source 10)", result.SkippedSources)
	}

	// (a) reached + captured; (b) read failed; (c) reached but empty (still a row).
	assertSeedState(t, seedState(ctx, t, client, 42), wantSeedState{ok: true, readAtSet: true, lastErr: "", name: "Comix"})
	assertSeedState(t, seedState(ctx, t, client, 10), wantSeedState{ok: false, readAtSet: false, lastErr: "source offline", name: "Asura Scans"})
	assertSeedState(t, seedState(ctx, t, client, 77), wantSeedState{ok: true, readAtSet: true, lastErr: "", name: "Weeb Central"})
	if n := client.SourcePreference.Query().Where(entsourcepreference.SourceID(77)).CountX(ctx); n != 0 {
		t.Errorf("source 77 SourcePreference rows = %d, want 0 (benign-empty)", n)
	}
}

// wantSeedState is the expected SourceSeedState shape asserted by assertSeedState.
type wantSeedState struct {
	ok        bool
	readAtSet bool
	lastErr   string
	name      string
}

// assertSeedState checks one SourceSeedState row against want, reported per
// field. Extracted so the multi-source test bodies stay within the cyclop gate.
func assertSeedState(t *testing.T, got *ent.SourceSeedState, want wantSeedState) {
	t.Helper()
	if got.PrefsReadOk != want.ok {
		t.Errorf("source %d prefs_read_ok = %t, want %t", got.SourceID, got.PrefsReadOk, want.ok)
	}
	if (got.PrefsReadAt != nil) != want.readAtSet {
		t.Errorf("source %d prefs_read_at set = %t, want %t", got.SourceID, got.PrefsReadAt != nil, want.readAtSet)
	}
	if got.LastError != want.lastErr {
		t.Errorf("source %d last_error = %q, want %q", got.SourceID, got.LastError, want.lastErr)
	}
	if got.SourceName != want.name {
		t.Errorf("source %d source_name = %q, want %q", got.SourceID, got.SourceName, want.name)
	}
}

// TestSeedSourcePreferences_SeedStateSelfHeals proves a source whose read FAILED
// on the first pass flips to ok=true with a cleared last_error once it succeeds
// on a later pass — the same row updated in place (idempotent, at most one row).
func TestSeedSourcePreferences_SeedStateSelfHeals(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedNamedProvider(ctx, t, client, "Solo Leveling", "55", "Comix")

	fc := &fakeClient{prefsErrBySource: map[string]error{"55": errors.New("boom")}}
	if _, err := enginetopo.SeedSourcePreferences(ctx, fc, client); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	first := seedState(ctx, t, client, 55)
	if first.PrefsReadOk || first.LastError != "boom" {
		t.Fatalf("first pass seed-state = %+v, want ok=false, error=boom", first)
	}

	// Second pass: the source now answers cleanly.
	delete(fc.prefsErrBySource, "55")
	fc.prefsBySource = map[string][]suwayomi.SourcePreference{
		"55": {{Type: suwayomi.PreferenceCheckBox, Position: 0, Key: "nsfw", CurrentBool: boolPtr(true)}},
	}
	if _, err := enginetopo.SeedSourcePreferences(ctx, fc, client); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	rows := client.SourceSeedState.Query().Where(entsourceseedstate.SourceID(55)).AllX(ctx)
	if len(rows) != 1 {
		t.Fatalf("got %d seed-state rows for source 55, want exactly 1 (upsert, not duplicate)", len(rows))
	}
	healed := rows[0]
	if healed.ID != first.ID {
		t.Error("second pass created a NEW seed-state row instead of updating the existing one")
	}
	if !healed.PrefsReadOk || healed.LastError != "" || healed.PrefsReadAt == nil {
		t.Errorf("healed seed-state = %+v, want ok=true, no error, read_at set", healed)
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
	// A disk-origin provider gets NO seed-state row (it is skipped before the read).
	if n := client.SourceSeedState.Query().CountX(ctx); n != 0 {
		t.Errorf("SourceSeedState rows = %d, want 0 for a disk-origin provider", n)
	}
}
