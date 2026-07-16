package enginetopo_test

import (
	"context"
	"errors"
	"testing"

	entsourcepreference "github.com/technobecet/tsundoku/internal/ent/sourcepreference"
	entsourceseedstate "github.com/technobecet/tsundoku/internal/ent/sourceseedstate"

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

// TestSeedSourcePreferences_MultiSelectCanonicalizedSorted proves a
// MultiSelect preference's captured value is SORTED before being stored,
// regardless of the engine's native (unstable, Set<String>-backed) order —
// the multiselect set-order canonicalization hardening carried forward from
// the opus review of slice 5. A later reconcile pass's live-side re-capture
// of the SAME set (in yet another order) then encodes byte-identically to
// this stored row, so prefInSync recognizes it as in-sync (see
// TestReconcile_MultiSelectOrderInsensitiveInSync in reconcile_test.go).
func TestSeedSourcePreferences_MultiSelectCanonicalizedSorted(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedProvider(ctx, t, client, "Solo Leveling", "42", 1001)

	fc := sourceenginefake.New(sourceenginefake.WithPreferences(42, []sourceengine.Preference{
		{Type: sourceengine.PreferenceMultiSelect, Key: "blockedGroups", CurrentValue: []string{"GroupC", "GroupA", "GroupB"}},
	}))

	if _, err := enginetopo.SeedSourcePreferences(ctx, fc, client); err != nil {
		t.Fatalf("SeedSourcePreferences: %v", err)
	}

	row := client.SourcePreference.Query().
		Where(entsourcepreference.SourceID(42), entsourcepreference.Key("blockedGroups")).
		OnlyX(ctx)
	const want = `["GroupA","GroupB","GroupC"]`
	if row.Value != want {
		t.Errorf("stored value = %q, want sorted %q", row.Value, want)
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
	// A disk-origin provider gets NO seed-state row (it is skipped before the read).
	if n := client.SourceSeedState.Query().CountX(ctx); n != 0 {
		t.Errorf("SourceSeedState rows = %d, want 0 for a disk-origin provider", n)
	}
}

// seedState fetches the single SourceSeedState row for a source id (fails the
// test if absent).
func seedState(ctx context.Context, t *testing.T, client *ent.Client, sourceID int64) *ent.SourceSeedState {
	t.Helper()
	return client.SourceSeedState.Query().
		Where(entsourceseedstate.SourceID(sourceID)).
		OnlyX(ctx)
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

	fc := &prefsErrClient{
		Client: sourceenginefake.New(
			sourceenginefake.WithPreferences(42, []sourceengine.Preference{
				{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: true},
			}),
			// Source 77 answers with an UNSET preference → zero SourcePreference
			// rows, but the read itself SUCCEEDED (ok=true).
			sourceenginefake.WithPreferences(77, []sourceengine.Preference{
				{Type: sourceengine.PreferenceEditText, Key: "username", CurrentValue: nil},
			}),
		),
		errBySource: map[int64]error{10: errors.New("source offline")},
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

// TestSeedSourcePreferences_SeedStateSelfHeals proves a source whose read FAILED
// on the first pass flips to ok=true with a cleared last_error once it succeeds
// on a later pass — the same row updated in place (idempotent, at most one row).
func TestSeedSourcePreferences_SeedStateSelfHeals(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	seedNamedProvider(ctx, t, client, "Solo Leveling", "55", "Comix")

	// The source has a real preference, but the wrapper makes the READ error on
	// the first pass; clearing the injected error lets the second pass succeed.
	fc := &prefsErrClient{
		Client: sourceenginefake.New(sourceenginefake.WithPreferences(55, []sourceengine.Preference{
			{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: true},
		})),
		errBySource: map[int64]error{55: errors.New("boom")},
	}
	if _, err := enginetopo.SeedSourcePreferences(ctx, fc, client); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	first := seedState(ctx, t, client, 55)
	if first.PrefsReadOk || first.LastError != "boom" {
		t.Fatalf("first pass seed-state = %+v, want ok=false, error=boom", first)
	}

	// Second pass: the source now answers cleanly.
	delete(fc.errBySource, 55)
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

// TestSeedSourcePreferences_DeterministicSourceName PINS the L3 stable-name pick:
// when the SAME numeric source appears across SeriesProvider rows with DIVERGING
// provider_name values, the recorded SourceSeedState.source_name is the
// DETERMINISTIC choice (lexicographically smallest), never the nondeterministic
// last row Postgres returned. Regressing to last-write-wins or flipping the
// comparator would fail this.
func TestSeedSourcePreferences_DeterministicSourceName(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	// Two live rows for the SAME numeric source "42", names out of sorted order.
	seedNamedProvider(ctx, t, client, "Solo Leveling", "42", "Zeta Name")
	seedNamedProvider(ctx, t, client, "Omniscient Reader", "42", "Alpha Name")

	fc := sourceenginefake.New(sourceenginefake.WithPreferences(42, []sourceengine.Preference{
		{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: true},
	}))

	if _, err := enginetopo.SeedSourcePreferences(ctx, fc, client); err != nil {
		t.Fatalf("SeedSourcePreferences: %v", err)
	}

	// Exactly one seed-state row, its name the lexicographically smallest.
	rows := client.SourceSeedState.Query().Where(entsourceseedstate.SourceID(42)).AllX(ctx)
	if len(rows) != 1 {
		t.Fatalf("got %d seed-state rows for source 42, want exactly 1", len(rows))
	}
	if rows[0].SourceName != "Alpha Name" {
		t.Errorf("source_name = %q, want %q (deterministic smallest, not last-write-wins)", rows[0].SourceName, "Alpha Name")
	}
}

// TestSeedSourcePreferences_SeedStateWriteFailureIsNonFatal PINS the best-effort
// contract: if the SourceSeedState upsert itself fails, the seed's CORE job is
// unaffected. It forces ONLY the seed-state write to fail — by DROPping the
// source_seed_states table out from under the pass while source_preferences is
// intact — then asserts (a) SeedSourcePreferences returns nil error, (b) the
// SourcePreference rows for a successful source are STILL written, (c) the
// Seeded/SkippedSources counts are intact. Removing the log-and-continue guard
// (propagating the upsert error) would fail this test.
func TestSeedSourcePreferences_SeedStateWriteFailureIsNonFatal(t *testing.T) {
	ctx := context.Background()
	client, db := testdb.NewWithSQL(t)

	seedNamedProvider(ctx, t, client, "Solo Leveling", "42", "Comix")           // read OK, has a pref
	seedNamedProvider(ctx, t, client, "Omniscient Reader", "10", "Asura Scans") // read errors

	fc := &prefsErrClient{
		Client: sourceenginefake.New(sourceenginefake.WithPreferences(42, []sourceengine.Preference{
			{Type: sourceengine.PreferenceCheckBox, Key: "nsfw", CurrentValue: true},
		})),
		errBySource: map[int64]error{10: errors.New("source offline")},
	}

	// Remove the seed-state table so EVERY SourceSeedState upsert errors, while the
	// source_preferences table (the seed's real output) stays writable.
	if _, err := db.ExecContext(ctx, `DROP TABLE source_seed_states`); err != nil {
		t.Fatalf("DROP TABLE source_seed_states: %v", err)
	}

	result, err := enginetopo.SeedSourcePreferences(ctx, fc, client)
	if err != nil {
		t.Fatalf("SeedSourcePreferences returned error despite best-effort seed-state write: %v", err)
	}
	if result.Seeded != 1 {
		t.Errorf("Seeded = %d, want 1 (source 42's nsfw still written)", result.Seeded)
	}
	if result.SkippedSources != 1 {
		t.Errorf("SkippedSources = %d, want 1 (source 10)", result.SkippedSources)
	}

	// The core output — source 42's preference — must be present regardless of the
	// seed-state write failure.
	rows := client.SourcePreference.Query().Where(entsourcepreference.SourceID(42)).AllX(ctx)
	if len(rows) != 1 || rows[0].Key != "nsfw" || rows[0].Value != "true" {
		t.Errorf("source 42 SourcePreference rows = %+v, want exactly 1 (nsfw=true)", rows)
	}
}
