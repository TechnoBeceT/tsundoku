// Package settings_test exercises the runtime-tunable settings overlay against an
// ephemeral PostgreSQL instance (testdb). Tests require Docker.
package settings_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/runtimepolicy"
	"github.com/technobecet/tsundoku/internal/settings"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

func TestSetManyRejectsGlobalSessionClearWithoutChangingSettingOrIntent(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	plain := settings.NewService(client, testDefaults())
	if err := plain.Set(ctx, settings.KeyFlareSolverrSessionName, "named"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SourceTransportPolicy.Create().SetSourceID(42).SetReuseBypassSession(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := plain.RuntimeIntent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	svc := settings.NewService(client, testDefaults()).WithRuntimePolicyCoordinator(runtimepolicy.New(client, ""))
	if err := svc.Set(ctx, settings.KeyFlareSolverrSessionName, ""); !errors.Is(err, runtimepolicy.ErrInvalidSelection) {
		t.Fatalf("Set error = %v, want ErrInvalidSelection", err)
	}
	if got := svc.FlareSolverrSessionName(ctx); got != "named" {
		t.Fatalf("session = %q, want named", got)
	}
	after, err := svc.RuntimeIntent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.DesiredRevision != before.DesiredRevision {
		t.Fatalf("desired revision = %d, want unchanged %d", after.DesiredRevision, before.DesiredRevision)
	}
}

func TestRuntimeApplyWaitDoesNotHoldPolicyMutationGate(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	coordinator := runtimepolicy.New(client, "")
	applyEntered := make(chan struct{})
	releaseApply := make(chan struct{})
	svc := settings.NewService(client, testDefaults()).
		WithRuntimePolicyCoordinator(coordinator).
		WithRuntimeConverger(runtimeConvergerFunc(func(context.Context) error {
			close(applyEntered)
			<-releaseApply
			return nil
		}))
	setDone := make(chan error, 1)
	go func() { setDone <- svc.Set(ctx, settings.KeyImpersonateURL, "http://gateway:8191") }()
	<-applyEntered
	gateDone := make(chan error, 1)
	go func() {
		gateDone <- coordinator.Mutate(ctx, runtimepolicy.Proposal{}, func(context.Context) error { return nil })
	}()
	select {
	case err := <-gateDone:
		if err != nil {
			t.Fatalf("second gate admission: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime apply retained policy mutation gate")
	}
	close(releaseApply)
	if err := <-setDone; err != nil {
		t.Fatal(err)
	}
}

func TestNonSessionRuntimeSettingWaitsForPolicyMutationGate(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	coordinator := runtimepolicy.New(client, "")
	entered := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = coordinator.Mutate(ctx, runtimepolicy.Proposal{}, func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	svc := settings.NewService(client, testDefaults()).WithRuntimePolicyCoordinator(coordinator)
	done := make(chan error, 1)
	go func() { done <- svc.Set(ctx, settings.KeyImpersonateURL, "http://gateway:8191") }()
	select {
	case err := <-done:
		t.Fatalf("runtime setting bypassed gate: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type runtimeConvergerFunc func(context.Context) error

func (f runtimeConvergerFunc) ReconcileRuntime(ctx context.Context) error { return f(ctx) }

type lifecycleRuntimeConverger struct {
	coordinator *enginetopo.SourceRuntimeApplier
	apply       func(context.Context) error
}

func (c lifecycleRuntimeConverger) ReconcileRuntime(ctx context.Context) error {
	return c.apply(ctx)
}

func (c lifecycleRuntimeConverger) RunRuntime(ctx context.Context, run func(context.Context) error) error {
	return c.coordinator.RunRuntime(ctx, run)
}

// testDefaults mirrors the config defaults so resolution tests are meaningful.
func testDefaults() settings.Defaults {
	return settings.Defaults{
		DownloadInterval:         15 * time.Minute,
		DownloadConcurrency:      5,
		MaxConcurrentDownloads:   6,
		RefreshInterval:          2 * time.Hour,
		RefreshConcurrency:       4,
		MaxRetries:               3,
		RetryBackoff:             time.Minute,
		LockedRetryInterval:      72 * time.Hour,
		StaleGraceDays:           14,
		StalledThresholdDays:     30,
		ExtensionCheckInterval:   24 * time.Hour,
		WarmupInterval:           15 * time.Minute,
		WarmupSlowThresholdMs:    5000,
		EngineSuperviseInterval:  30 * time.Second,
		SearchCacheTTL:           time.Hour,
		ChapterCacheTTL:          time.Hour,
		SourcesFailureThreshold:  5,
		SourcesCooldown:          30 * time.Minute,
		SourcesMinRequestDelay:   500 * time.Millisecond,
		SourcesImageRequestDelay: 500 * time.Millisecond,
		SuppressSplitParts:       true,
		TrackRetryInterval:       5 * time.Minute,
		MetadataAutoIdentify:     true,
		FlareSolverrEnabled:      false,
		FlareSolverrURL:          "",
		FlareSolverrTimeout:      60,
		FlareSolverrSessionName:  "",
		FlareSolverrSessionTTL:   15,
		NotificationsEnabled:     true,
		EngineSocksEnabled:       false,
		EngineSocksHost:          "",
		EngineSocksPort:          1080,
		EngineSocksVersion:       5,
		RetainedVersions:         3,
	}
}

func TestRuntimeSettingsFailureLeavesDurablePendingIntent(t *testing.T) {
	ctx := context.Background()
	svc := settings.NewService(testdb.New(t), testDefaults()).WithRuntimeConverger(
		runtimeConvergerFunc(func(context.Context) error { return errors.New("profile unavailable") }),
	)

	if err := svc.Set(ctx, settings.KeyImpersonateEnabled, "true"); err != nil {
		t.Fatalf("Set must preserve persisted-success contract: %v", err)
	}
	intent, err := svc.RuntimeIntent(ctx)
	if err != nil {
		t.Fatalf("RuntimeIntent: %v", err)
	}
	if intent.DesiredRevision != 1 || intent.AppliedRevision != 0 || intent.LastApplyAttempt == nil {
		t.Fatalf("intent after failed convergence = %+v, want desired 1 / applied 0 with attempt", intent)
	}
	if !strings.Contains(intent.LastApplyError, "profile unavailable") {
		t.Fatalf("last apply error = %q, want persisted failure", intent.LastApplyError)
	}
}

func TestReconcilePendingRetriesSettingsOnlyIntentWithoutSourceRows(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	beforeRestart := settings.NewService(client, testDefaults())
	if err := beforeRestart.Set(ctx, settings.KeyEngineSocksEnabled, "true"); err != nil {
		t.Fatalf("persist settings-only intent: %v", err)
	}
	if got := client.SourceRuntimeIntent.Query().CountX(ctx); got != 0 {
		t.Fatalf("source runtime intents = %d, want zero for global settings work", got)
	}

	calls := 0
	afterRestart := settings.NewService(client, testDefaults()).WithRuntimeConverger(
		runtimeConvergerFunc(func(context.Context) error {
			calls++
			return nil
		}),
	)
	if err := afterRestart.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending: %v", err)
	}
	intent, err := afterRestart.RuntimeIntent(ctx)
	if err != nil {
		t.Fatalf("RuntimeIntent: %v", err)
	}
	if calls != 1 || intent.DesiredRevision != 1 || intent.AppliedRevision != 1 {
		t.Fatalf("startup retry calls=%d intent=%+v, want one apply and revision 1 acknowledged", calls, intent)
	}
}

func TestRuntimeSettingsNewerRevisionDuringApplyRemainsPending(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	plain := settings.NewService(client, testDefaults())
	var newerErr error
	svc := settings.NewService(client, testDefaults()).WithRuntimeConverger(
		runtimeConvergerFunc(func(context.Context) error {
			newerErr = plain.Set(ctx, settings.KeyImpersonateURL, "http://newer.test:8788")
			return nil
		}),
	)

	if err := svc.Set(ctx, settings.KeyImpersonateEnabled, "true"); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if newerErr != nil {
		t.Fatalf("newer settings revision during apply: %v", newerErr)
	}
	intent, err := svc.RuntimeIntent(ctx)
	if err != nil {
		t.Fatalf("RuntimeIntent: %v", err)
	}
	if intent.DesiredRevision != 2 || intent.AppliedRevision != 0 {
		t.Fatalf("intent after revision race = %+v, want newer desired 2 still pending", intent)
	}
}

func TestRuntimeSettingsRollbackLeavesNoIntentRevision(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	client.Settings.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if m, ok := mutation.(*ent.SettingsMutation); ok && m.Op().Is(ent.OpCreate) {
				return nil, errors.New("injected settings write failure")
			}
			return next.Mutate(ctx, mutation)
		})
	})
	svc := settings.NewService(client, testDefaults())

	if err := svc.Set(ctx, settings.KeyImpersonateEnabled, "true"); err == nil {
		t.Fatal("Set error = nil, want injected write failure")
	}
	intent, err := svc.RuntimeIntent(ctx)
	if err != nil {
		t.Fatalf("RuntimeIntent: %v", err)
	}
	if intent.DesiredRevision != 0 || intent.AppliedRevision != 0 {
		t.Fatalf("intent after rolled-back settings transaction = %+v, want zero revision", intent)
	}
	if got := client.GlobalRuntimeIntent.Query().CountX(ctx); got != 0 {
		t.Fatalf("global runtime intent rows after rollback = %d, want zero", got)
	}
	if got := svc.ImpersonateEnabled(ctx); got {
		t.Fatal("impersonate setting persisted despite transaction rollback")
	}
}

func TestEnsureRuntimeIntentBackfillsOnlyExistingRuntimeSettings(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	if err := settings.EnsureRuntimeIntent(ctx, client); err != nil {
		t.Fatalf("EnsureRuntimeIntent on empty install: %v", err)
	}
	if got := client.GlobalRuntimeIntent.Query().CountX(ctx); got != 0 {
		t.Fatalf("global intents on empty install = %d, want zero", got)
	}

	client.Settings.Create().
		SetKey(settings.KeyImpersonateEnabled).
		SetValue("true").
		SaveX(ctx)
	for i := 0; i < 2; i++ {
		if err := settings.EnsureRuntimeIntent(ctx, client); err != nil {
			t.Fatalf("EnsureRuntimeIntent pass %d: %v", i+1, err)
		}
	}
	intent, err := settings.NewService(client, testDefaults()).RuntimeIntent(ctx)
	if err != nil {
		t.Fatalf("RuntimeIntent: %v", err)
	}
	if intent.DesiredRevision != 1 || intent.AppliedRevision != 0 {
		t.Fatalf("backfilled intent = %+v, want desired 1 / applied 0", intent)
	}
	if got := client.GlobalRuntimeIntent.Query().CountX(ctx); got != 1 {
		t.Fatalf("global intent rows after two passes = %d, want one", got)
	}
	if got := client.SourceRuntimeIntent.Query().CountX(ctx); got != 0 {
		t.Fatalf("source intents after global backfill = %d, want zero", got)
	}
}

func TestBackfilledRuntimeIntentSurvivesFailedFirstRestore(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	client.Settings.Create().
		SetKey(settings.KeyEngineSocksEnabled).
		SetValue("true").
		SaveX(ctx)
	if err := settings.EnsureRuntimeIntent(ctx, client); err != nil {
		t.Fatalf("EnsureRuntimeIntent: %v", err)
	}

	svc := settings.NewService(client, testDefaults()).WithRuntimeConverger(
		runtimeConvergerFunc(func(context.Context) error { return errors.New("engine unavailable") }),
	)
	if err := svc.ReconcilePending(ctx); err == nil {
		t.Fatal("ReconcilePending error = nil, want failed first restore")
	}
	intent, err := svc.RuntimeIntent(ctx)
	if err != nil {
		t.Fatalf("RuntimeIntent: %v", err)
	}
	if intent.DesiredRevision != 1 || intent.AppliedRevision != 0 || intent.LastApplyAttempt == nil || !strings.Contains(intent.LastApplyError, "engine unavailable") {
		t.Fatalf("intent after failed first restore = %+v, want durable pending failure", intent)
	}
}

func TestCanceledRuntimeApplyPersistsBoundedAttemptMetadata(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	plain := settings.NewService(client, testDefaults())
	if err := plain.Set(ctx, settings.KeyImpersonateEnabled, "true"); err != nil {
		t.Fatalf("persist pending runtime setting: %v", err)
	}

	started := make(chan struct{})
	svc := settings.NewService(client, testDefaults()).WithRuntimeConverger(
		runtimeConvergerFunc(func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return errors.New(" apply cancelled\r\n" + strings.Repeat("x", 600))
		}),
	)
	applyCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := svc.ApplyPending(applyCtx); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ApplyPending error = nil, want cancellation failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ApplyPending did not finish within detached metadata bound")
	}

	intent, err := svc.RuntimeIntent(ctx)
	if err != nil {
		t.Fatalf("RuntimeIntent: %v", err)
	}
	if intent.DesiredRevision != 1 || intent.AppliedRevision != 0 || intent.LastApplyAttempt == nil {
		t.Fatalf("intent after canceled apply = %+v, want pending revision with attempt", intent)
	}
	if intent.LastApplyError == "" || len(intent.LastApplyError) > 512 || strings.ContainsAny(intent.LastApplyError, "\r\n") {
		t.Fatalf("last apply error = %q, want sanitized bounded metadata", intent.LastApplyError)
	}
}

func TestCanceledRuntimeApplyCannotHoldShutdownOnMetadataWrite(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	plain := settings.NewService(client, testDefaults())
	if err := plain.Set(ctx, settings.KeyImpersonateEnabled, "true"); err != nil {
		t.Fatalf("persist pending runtime setting: %v", err)
	}
	client.GlobalRuntimeIntent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if m, ok := mutation.(*ent.GlobalRuntimeIntentMutation); ok {
				if _, metadataWrite := m.LastApplyError(); metadataWrite {
					<-ctx.Done()
					return nil, ctx.Err()
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})

	started := make(chan struct{})
	svc := settings.NewService(client, testDefaults()).WithRuntimeConverger(
		runtimeConvergerFunc(func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}),
	)
	applyCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := svc.ApplyPending(applyCtx); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ApplyPending error = nil, want canceled apply and metadata timeout")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("ApplyPending held shutdown for %v, want bounded under 2s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ApplyPending held shutdown beyond detached metadata timeout")
	}
	intent, err := svc.RuntimeIntent(ctx)
	if err != nil {
		t.Fatalf("RuntimeIntent: %v", err)
	}
	if intent.DesiredRevision != 1 || intent.AppliedRevision != 0 {
		t.Fatalf("intent after metadata timeout = %+v, want revision 1 pending", intent)
	}
}

func TestRuntimeConvergenceShutdownJoinsSettingsMetadataTail(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	plain := settings.NewService(client, testDefaults())
	if err := plain.Set(ctx, settings.KeyImpersonateEnabled, "true"); err != nil {
		t.Fatalf("persist pending runtime setting: %v", err)
	}
	metadataEntered := make(chan struct{})
	releaseMetadata := make(chan struct{})
	client.GlobalRuntimeIntent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if m, ok := mutation.(*ent.GlobalRuntimeIntentMutation); ok {
				if _, metadataWrite := m.LastApplyError(); metadataWrite {
					close(metadataEntered)
					<-releaseMetadata
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})

	coordinator := enginetopo.NewSourceRuntimeApplier(sourceenginefake.New(), enginetopo.NetworkReconcileDeps{})
	applyEntered := make(chan struct{})
	svc := settings.NewService(client, testDefaults()).WithRuntimeConverger(lifecycleRuntimeConverger{
		coordinator: coordinator,
		apply: func(ctx context.Context) error {
			close(applyEntered)
			<-ctx.Done()
			return ctx.Err()
		},
	})
	applyDone := make(chan error, 1)
	go func() {
		_, err := svc.ApplyPending(ctx)
		applyDone <- err
	}()
	<-applyEntered

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- coordinator.ShutdownRuntimeConvergence(context.Background()) }()
	select {
	case <-metadataEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("settings apply did not enter detached metadata tail during convergence shutdown")
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("convergence shutdown returned before settings metadata tail: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseMetadata)
	select {
	case err := <-applyDone:
		if err == nil {
			t.Fatal("ApplyPending error = nil, want lifecycle cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("settings ApplyPending did not finish after metadata release")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("ShutdownRuntimeConvergence: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("convergence shutdown did not join settings metadata tail")
	}
}

func TestFailedRuntimeApplyDoesNotOverwriteNewerRevisionMetadata(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	plain := settings.NewService(client, testDefaults())
	if err := plain.Set(ctx, settings.KeyImpersonateEnabled, "true"); err != nil {
		t.Fatalf("persist first runtime revision: %v", err)
	}

	var newerErr error
	svc := settings.NewService(client, testDefaults()).WithRuntimeConverger(
		runtimeConvergerFunc(func(context.Context) error {
			newerErr = plain.Set(ctx, settings.KeyImpersonateURL, "http://newer.test:8788")
			return errors.New("obsolete revision failed")
		}),
	)
	if _, err := svc.ApplyPending(ctx); err == nil {
		t.Fatal("ApplyPending error = nil, want obsolete apply failure")
	}
	if newerErr != nil {
		t.Fatalf("persist newer revision: %v", newerErr)
	}
	intent, err := svc.RuntimeIntent(ctx)
	if err != nil {
		t.Fatalf("RuntimeIntent: %v", err)
	}
	if intent.DesiredRevision != 2 || intent.AppliedRevision != 0 || intent.LastApplyAttempt != nil || intent.LastApplyError != "" {
		t.Fatalf("newer intent metadata = %+v, want untouched pending revision 2", intent)
	}
}

func TestRuntimeConfigSnapshotReadsOneCommittedDatabaseState(t *testing.T) {
	ctx := context.Background()
	client, sqlDB := testdb.NewWithSQL(t)
	svc := settings.NewService(client, testDefaults())
	old := runtimeSettingValues("old", false, 11)
	next := runtimeSettingValues("new", true, 22)
	if err := svc.SetMany(ctx, old); err != nil {
		t.Fatalf("seed old runtime settings: %v", err)
	}

	queries := 0
	client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			queries++
			return next.Query(ctx, query)
		})
	}))

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin concurrent runtime update: %v", err)
	}
	for _, update := range next {
		if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = $1 WHERE key = $2`, update.Value, update.Key); err != nil {
			_ = tx.Rollback()
			t.Fatalf("stage %s: %v", update.Key, err)
		}
	}

	queries = 0
	before, err := svc.RuntimeConfigSnapshot(ctx)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("RuntimeConfigSnapshot before commit: %v", err)
	}
	if queries != 1 {
		_ = tx.Rollback()
		t.Fatalf("runtime snapshot queries = %d, want exactly 1", queries)
	}
	assertRuntimeSnapshot(t, before, "old", false, 11)

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit concurrent runtime update: %v", err)
	}
	queries = 0
	after, err := svc.RuntimeConfigSnapshot(ctx)
	if err != nil {
		t.Fatalf("RuntimeConfigSnapshot after commit: %v", err)
	}
	if queries != 1 {
		t.Fatalf("runtime snapshot queries after commit = %d, want exactly 1", queries)
	}
	assertRuntimeSnapshot(t, after, "new", true, 22)
}

func runtimeSettingValues(label string, enabled bool, sourceID int64) []settings.KeyValue {
	return []settings.KeyValue{
		{Key: settings.KeyFlareSolverrEnabled, Value: strconv.FormatBool(enabled)},
		{Key: settings.KeyFlareSolverrURL, Value: "http://" + label + "-flare:8191"},
		{Key: settings.KeyFlareSolverrTimeout, Value: map[bool]string{false: "60", true: "90"}[enabled]},
		{Key: settings.KeyFlareSolverrSessionName, Value: label + "-session"},
		{Key: settings.KeyFlareSolverrSessionTTL, Value: map[bool]string{false: "15", true: "30"}[enabled]},
		{Key: settings.KeyFlareSolverrResponseFallback, Value: strconv.FormatBool(enabled)},
		{Key: settings.KeyEngineSocksEnabled, Value: strconv.FormatBool(enabled)},
		{Key: settings.KeyEngineSocksHost, Value: label + "-socks"},
		{Key: settings.KeyEngineSocksPort, Value: map[bool]string{false: "1080", true: "1081"}[enabled]},
		{Key: settings.KeyEngineSocksVersion, Value: map[bool]string{false: "5", true: "4"}[enabled]},
		{Key: settings.KeyImpersonateEnabled, Value: strconv.FormatBool(enabled)},
		{Key: settings.KeyImpersonateURL, Value: "http://" + label + "-impersonate:8788"},
		{Key: settings.KeyImpersonateSources, Value: strconv.FormatInt(sourceID, 10)},
	}
}

func assertRuntimeSnapshot(t *testing.T, got settings.RuntimeConfigSnapshot, label string, enabled bool, sourceID int64) {
	t.Helper()
	wantTimeout, wantTTL, wantPort, wantVersion := 60, 15, 1080, 5
	if enabled {
		wantTimeout, wantTTL, wantPort, wantVersion = 90, 30, 1081, 4
	}
	if got.FlareSolverrEnabled != enabled || got.FlareSolverrURL != "http://"+label+"-flare:8191" ||
		got.FlareSolverrTimeout != wantTimeout || got.FlareSolverrSessionName != label+"-session" ||
		got.FlareSolverrSessionTTL != wantTTL || got.FlareSolverrResponseFallback != enabled ||
		got.EngineSocksEnabled != enabled || got.EngineSocksHost != label+"-socks" ||
		got.EngineSocksPort != wantPort || got.EngineSocksVersion != wantVersion ||
		got.ImpersonateEnabled != enabled || got.ImpersonateURL != "http://"+label+"-impersonate:8788" ||
		len(got.ImpersonateSources) != 1 || got.ImpersonateSources[0] != sourceID {
		t.Fatalf("runtime snapshot = %+v, want complete %s committed state", got, label)
	}
}

// assertIntTunable exercises one int-typed tunable end-to-end: its injected
// default, a valid override hot-reloading through the typed accessor, and
// fail-closed rejection of a below-min AND an above-max value. Shared by the
// int-tunable tests so the identical default→set→bounds pattern lives once
// (§2 DRY — extracting it also removes the dupl the copies would otherwise be).
// get selects the accessor under test so the SAME body covers any int key.
func assertIntTunable(t *testing.T, key string, get func(*settings.Service, context.Context) int, def, override, belowMin, aboveMax int) {
	t.Helper()
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := get(svc, ctx); got != def {
		t.Errorf("%s default = %d, want %d", key, got, def)
	}
	if err := svc.Set(ctx, key, strconv.Itoa(override)); err != nil {
		t.Fatalf("Set(%d): %v", override, err)
	}
	if got := get(svc, ctx); got != override {
		t.Errorf("after Set, %s = %d, want %d (read-at-use hot reload)", key, got, override)
	}
	if err := svc.Set(ctx, key, strconv.Itoa(belowMin)); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Errorf("Set(%d) below the min: err = %v, want ErrInvalidSetting", belowMin, err)
	}
	if err := svc.Set(ctx, key, strconv.Itoa(aboveMax)); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Errorf("Set(%d) above the max: err = %v, want ErrInvalidSetting", aboveMax, err)
	}
}

// assertBoolTunable exercises one bool-typed tunable end-to-end: its injected
// default, the OPPOSITE value round-tripping through Set (proving the accessor
// reads at use rather than caching the default), and fail-closed rejection of a
// value that is not a bool at all. Shared by the bool-tunable tests because all
// of them pin that one three-part contract and differ only in key + accessor
// (§2 DRY — extracting it also removes the dupl the copies would otherwise be).
// get selects the accessor under test so the SAME body covers any bool key.
func assertBoolTunable(t *testing.T, key string, get func(*settings.Service, context.Context) bool, def bool) {
	t.Helper()
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := get(svc, ctx); got != def {
		t.Fatalf("%s default = %t, want %t", key, got, def)
	}
	if err := svc.Set(ctx, key, strconv.FormatBool(!def)); err != nil {
		t.Fatalf("Set(%t): %v", !def, err)
	}
	if got := get(svc, ctx); got == def {
		t.Fatalf("after Set(%t), %s = %t, want %t (read-at-use hot reload)", !def, key, got, !def)
	}
	if err := svc.Set(ctx, key, "notabool"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set non-bool: err = %v, want ErrInvalidSetting", err)
	}
}

// assertMinOrZeroDurationTunable exercises one duration tunable registered with
// durationTunableMinOrZero — the "off, or at least the floor" shape. It pins all
// four parts of that contract: the injected default, 0 accepted as DISABLED (the
// owner's live off switch, which a plain min-bounded duration would reject), a
// positive value below the floor rejected fail-closed, and a valid override
// hot-reloading through the typed accessor. Mirrors assertIntTunable so the
// registry's shared validator has exactly one shared test body.
// get selects the accessor under test so the SAME body covers any such key.
// belowFloor is a RAW string, not a Duration, because each key's floor sits in a
// different unit and the rejected value is what an owner would actually type.
func assertMinOrZeroDurationTunable(
	t *testing.T,
	key string,
	get func(*settings.Service, context.Context) time.Duration,
	def time.Duration,
	belowFloor string,
	override time.Duration,
) {
	t.Helper()
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := get(svc, ctx); got != def {
		t.Errorf("%s default = %v, want %v", key, got, def)
	}
	// 0 = disabled is accepted.
	if err := svc.Set(ctx, key, "0"); err != nil {
		t.Fatalf("Set 0: %v", err)
	}
	if got := get(svc, ctx); got != 0 {
		t.Errorf("%s after Set 0 = %v, want 0 (disabled)", key, got)
	}
	// A positive value below the floor is rejected.
	if err := svc.Set(ctx, key, belowFloor); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set %s (below the floor): want ErrInvalidSetting, got %v", belowFloor, err)
	}
	// A valid override round-trips.
	if err := svc.Set(ctx, key, override.String()); err != nil {
		t.Fatalf("Set %v: %v", override, err)
	}
	if got := get(svc, ctx); got != override {
		t.Errorf("%s after Set %v = %v, want %v (read-at-use hot reload)", key, override, got, override)
	}
}

// TestRetainedVersions proves the extensions.retained_versions accessor returns
// the injected default, hot-reloads a valid override, and fail-closes out of
// bounds (1..20).
func TestRetainedVersions(t *testing.T) {
	assertIntTunable(t, settings.KeyRetainedVersions,
		func(s *settings.Service, ctx context.Context) int { return s.RetainedVersions(ctx) },
		3, 7, 0, 21)
}

// TestStalledThresholdDays proves the QCAT-297 health.stalled_threshold_days
// accessor returns the injected default (30), hot-reloads a valid override, and
// fail-closes out of bounds (1..365 — NOT 0, unlike stale_grace_days: a 0-day
// stalled window would flag the whole library).
func TestStalledThresholdDays(t *testing.T) {
	assertIntTunable(t, settings.KeyStalledThresholdDays,
		func(s *settings.Service, ctx context.Context) int { return s.StalledThresholdDays(ctx) },
		30, 45, 0, 366)
}

// TestAccessorsReturnDefaultsWhenNoRow proves every typed accessor falls back to
// the injected config default when the Settings table has no override.
func TestAccessorsReturnDefaultsWhenNoRow(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.DownloadInterval(ctx); got != 15*time.Minute {
		t.Errorf("DownloadInterval default = %v, want 15m", got)
	}
	if got := svc.RefreshInterval(ctx); got != 2*time.Hour {
		t.Errorf("RefreshInterval default = %v, want 2h", got)
	}
	if got := svc.DownloadConcurrency(ctx); got != 5 {
		t.Errorf("DownloadConcurrency default = %d, want 5", got)
	}
	if got := svc.RefreshConcurrency(ctx); got != 4 {
		t.Errorf("RefreshConcurrency default = %d, want 4", got)
	}
	if got := svc.MaxRetries(ctx); got != 3 {
		t.Errorf("MaxRetries default = %d, want 3", got)
	}
	if got := svc.RetryBackoff(ctx); got != time.Minute {
		t.Errorf("RetryBackoff default = %v, want 1m", got)
	}
	if got := svc.StaleGraceDays(ctx); got != 14 {
		t.Errorf("StaleGraceDays default = %d, want 14", got)
	}
	if got := svc.TrackRetryInterval(ctx); got != 5*time.Minute {
		t.Errorf("TrackRetryInterval default = %v, want 5m", got)
	}
}

// TestSetThenResolveTrackRetryInterval proves a Set override on the new
// tracker-retry tunable round-trips through its typed accessor, mirroring
// TestSetThenResolveDuration for the other duration tunables.
func TestSetThenResolveTrackRetryInterval(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyTrackRetryInterval, "2m"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := svc.TrackRetryInterval(ctx); got != 2*time.Minute {
		t.Errorf("after Set, TrackRetryInterval = %v, want 2m", got)
	}
	if err := svc.Set(ctx, settings.KeyTrackRetryInterval, "10s"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Errorf("Set(10s) below the 30s floor: err = %v, want ErrInvalidSetting", err)
	}
}

// TestLockedRetryInterval pins the whole jobs.locked_retry_interval path
// (GAP-141): the injected 72h default, fail-closed rejection of a value below the
// 1h floor, and a valid override hot-reloading through the typed accessor —
// mirroring the coverage its sibling jobs.retry_backoff already has.
//
// The accessor's KEY is asserted in BOTH directions on purpose. Resolving
// jobs.retry_backoff here is the silent, expensive regression: every withheld
// chapter would re-check on the 30-minute retry backoff instead of the 72-hour
// paywall horizon — roughly 144x more paywall hits per week, aimed at exactly the
// anti-bot-sensitive sources this interval exists to spare, with no error and no
// health signal to show for it.
//
// The floor matters for the same reason: a deferral burns no attempts and trips
// no breaker, so a too-short interval has nothing else to stop it.
func TestLockedRetryInterval(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.LockedRetryInterval(ctx); got != 72*time.Hour {
		t.Errorf("LockedRetryInterval default = %v, want 72h", got)
	}
	if err := svc.Set(ctx, settings.KeyLockedRetryInterval, "30m"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Errorf("Set(30m) below the 1h floor: err = %v, want ErrInvalidSetting", err)
	}
	if err := svc.Set(ctx, settings.KeyLockedRetryInterval, "48h"); err != nil {
		t.Fatalf("Set(48h): %v", err)
	}
	if got := svc.LockedRetryInterval(ctx); got != 48*time.Hour {
		t.Errorf("after Set, LockedRetryInterval = %v, want 48h (read-at-use hot reload)", got)
	}

	// Overriding the SIBLING key must move only the sibling: neither accessor may
	// resolve the other's value.
	if err := svc.Set(ctx, settings.KeyRetryBackoff, "2m"); err != nil {
		t.Fatalf("Set(%s, 2m): %v", settings.KeyRetryBackoff, err)
	}
	if got := svc.LockedRetryInterval(ctx); got != 48*time.Hour {
		t.Errorf("LockedRetryInterval = %v after overriding %s, want 48h — the accessor resolves the wrong key",
			got, settings.KeyRetryBackoff)
	}
	if got := svc.RetryBackoff(ctx); got != 2*time.Minute {
		t.Errorf("RetryBackoff = %v, want 2m — the locked-interval override leaked into the retry backoff", got)
	}
}

// TestSetThenResolveDuration proves a Set override is read back by the typed
// accessor (the read-at-use / hot-reload contract for durations).
func TestSetThenResolveDuration(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyDownloadInterval, "30m"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := svc.DownloadInterval(ctx); got != 30*time.Minute {
		t.Errorf("after Set, DownloadInterval = %v, want 30m", got)
	}
}

// TestSetThenResolveInt proves an int override round-trips through the accessor.
func TestSetThenResolveInt(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyMaxRetries, "7"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := svc.MaxRetries(ctx); got != 7 {
		t.Errorf("after Set, MaxRetries = %d, want 7", got)
	}
}

// TestSetIsIdempotentUpsert proves a second Set on the same key updates rather
// than duplicating (the key is unique; the second value wins).
func TestSetIsIdempotentUpsert(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyRefreshConcurrency, "8"); err != nil {
		t.Fatalf("Set 1: %v", err)
	}
	if err := svc.Set(ctx, settings.KeyRefreshConcurrency, "16"); err != nil {
		t.Fatalf("Set 2: %v", err)
	}
	if got := svc.RefreshConcurrency(ctx); got != 16 {
		t.Errorf("after re-Set, RefreshConcurrency = %d, want 16", got)
	}
}

// TestSetUnknownKey proves a key outside the allowlist is rejected (the API
// never writes arbitrary keys).
func TestSetUnknownKey(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())

	err := svc.Set(context.Background(), "jobs.secret_backdoor", "1")
	if !errors.Is(err, settings.ErrUnknownSetting) {
		t.Fatalf("want ErrUnknownSetting, got %v", err)
	}
}

// TestSetInvalidValue proves out-of-bounds / unparseable values are rejected for
// each value shape, so the store never holds an invalid value (fail-closed).
func TestSetInvalidValue(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	cases := []struct {
		name, key, value string
	}{
		{"duration below min", settings.KeyDownloadInterval, "5s"},
		{"refresh below min", settings.KeyRefreshInterval, "1m"},
		{"unparseable duration", settings.KeyRetryBackoff, "soon"},
		{"backoff below min", settings.KeyRetryBackoff, "0s"},
		{"retries negative", settings.KeyMaxRetries, "-1"},
		// 0 is rejected: a source must always get at least one attempt, else the
		// attempts>=maxRetries rule would drive the whole library to permanently_failed.
		{"retries zero", settings.KeyMaxRetries, "0"},
		{"retries over max", settings.KeyMaxRetries, "21"},
		{"concurrency zero", settings.KeyRefreshConcurrency, "0"},
		{"concurrency over max", settings.KeyRefreshConcurrency, "33"},
		{"unparseable int", settings.KeyMaxRetries, "lots"},
		{"days over max", settings.KeyStaleGraceDays, "366"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Set(ctx, tc.key, tc.value)
			if !errors.Is(err, settings.ErrInvalidSetting) {
				t.Fatalf("want ErrInvalidSetting, got %v", err)
			}
		})
	}

	// The rejected writes must not have persisted: the accessor still returns the
	// default.
	if got := svc.MaxRetries(ctx); got != 3 {
		t.Errorf("rejected writes leaked: MaxRetries = %d, want default 3", got)
	}
}

// TestSetManyAllOrNothing proves a batch with one bad key writes nothing.
func TestSetManyAllOrNothing(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	err := svc.SetMany(ctx, []settings.KeyValue{
		{Key: settings.KeyMaxRetries, Value: "9"},        // valid
		{Key: settings.KeyDownloadInterval, Value: "1s"}, // invalid (< 1m)
	})
	if !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("want ErrInvalidSetting, got %v", err)
	}
	// The valid update in the same batch must have been rolled back.
	if got := svc.MaxRetries(ctx); got != 3 {
		t.Errorf("partial batch write leaked: MaxRetries = %d, want default 3", got)
	}
}

// TestSetManyPersistsAll proves a fully-valid batch persists every update.
func TestSetManyPersistsAll(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	err := svc.SetMany(ctx, []settings.KeyValue{
		{Key: settings.KeyMaxRetries, Value: "9"},
		{Key: settings.KeyRetryBackoff, Value: "2m"},
	})
	if err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	if got := svc.MaxRetries(ctx); got != 9 {
		t.Errorf("MaxRetries = %d, want 9", got)
	}
	if got := svc.RetryBackoff(ctx); got != 2*time.Minute {
		t.Errorf("RetryBackoff = %v, want 2m", got)
	}
}

// TestListReflectsDefaultsAndOverrides proves List returns the whole allowlist in
// stable order, with current=default until an override is set, then current=value.
func TestListReflectsDefaultsAndOverrides(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	list := svc.List(ctx)
	if len(list) != 40 {
		t.Fatalf("List len = %d, want 40", len(list))
	}
	// Stable order: first row is download_interval.
	if list[0].Key != settings.KeyDownloadInterval {
		t.Errorf("List[0].Key = %q, want %q", list[0].Key, settings.KeyDownloadInterval)
	}
	assertAllRowsAtDefault(t, list)

	if err := svc.Set(ctx, settings.KeyDownloadInterval, "45m"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	dl := findSetting(t, svc.List(ctx), settings.KeyDownloadInterval)
	if dl.Value != "45m0s" {
		t.Errorf("download_interval value = %q, want 45m0s", dl.Value)
	}
	if dl.Default != "15m0s" {
		t.Errorf("download_interval default = %q, want 15m0s", dl.Default)
	}
}

// assertAllRowsAtDefault fails if any row's current value differs from its
// default or is missing type metadata. Unit is required for every type except
// bool (a bool tunable has no unit of measure).
func assertAllRowsAtDefault(t *testing.T, list []settings.SettingDTO) {
	t.Helper()
	for _, row := range list {
		if row.Value != row.Default {
			t.Errorf("%s: current %q != default %q before any override", row.Key, row.Value, row.Default)
		}
		if row.Type == "" {
			t.Errorf("%s: missing type %q", row.Key, row.Type)
		}
		if row.Unit == "" && row.Type != string(settings.TypeBool) {
			t.Errorf("%s: missing unit %q", row.Key, row.Unit)
		}
	}
}

// findSetting returns the row with the given key (failing the test if absent).
func findSetting(t *testing.T, list []settings.SettingDTO, key string) settings.SettingDTO {
	t.Helper()
	for _, row := range list {
		if row.Key == key {
			return row
		}
	}
	t.Fatalf("setting %q not found in list", key)
	return settings.SettingDTO{}
}

// TestStaticProviderReturnsFixedValues proves the Static provider satisfies the
// accessor surface and returns its constructed values (used by consumer tests).
func TestStaticProviderReturnsFixedValues(t *testing.T) {
	ctx := context.Background()
	s := settings.Static{
		Download: time.Second, DownloadConc: 6, Refresh: 2 * time.Second, Concurrency: 2,
		Retries: 5, Backoff: 3 * time.Second, LockedRetry: 96 * time.Hour, StaleGrace: 7,
		ExtCheck: 12 * time.Hour, WarmupIv: 15 * time.Minute, WarmupSlow: 4000,
		SourcesFailureThresh: 8, SourcesCooldownIv: 45 * time.Minute, SourcesMinDelay: 750 * time.Millisecond,
		ImageRequestDelayIv:      1250 * time.Millisecond,
		MetadataAutoIdentifyFlag: true,
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"DownloadInterval", s.DownloadInterval(ctx), time.Second},
		{"DownloadConcurrency", s.DownloadConcurrency(ctx), 6},
		{"RefreshInterval", s.RefreshInterval(ctx), 2 * time.Second},
		{"RefreshConcurrency", s.RefreshConcurrency(ctx), 2},
		{"MaxRetries", s.MaxRetries(ctx), 5},
		{"RetryBackoff", s.RetryBackoff(ctx), 3 * time.Second},
		// Deliberately far from Backoff: Static is what the dispatcher tests wire in,
		// so a LockedRetryInterval that silently returned the ordinary backoff would
		// make the deferral horizon indistinguishable from a cooldown.
		{"LockedRetryInterval", s.LockedRetryInterval(ctx), 96 * time.Hour},
		{"StaleGraceDays", s.StaleGraceDays(ctx), 7},
		{"ExtensionCheckInterval", s.ExtensionCheckInterval(ctx), 12 * time.Hour},
		{"WarmupInterval", s.WarmupInterval(ctx), 15 * time.Minute},
		{"WarmupSlowThresholdMs", s.WarmupSlowThresholdMs(ctx), 4000},
		{"SourcesFailureThreshold", s.SourcesFailureThreshold(ctx), 8},
		{"SourcesCooldown", s.SourcesCooldown(ctx), 45 * time.Minute},
		{"SourcesMinRequestDelay", s.SourcesMinRequestDelay(ctx), 750 * time.Millisecond},
		{"ImageRequestDelay", s.ImageRequestDelay(ctx), 1250 * time.Millisecond},
		{"MetadataAutoIdentify", s.MetadataAutoIdentify(ctx), true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("Static.%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestExtensionCheckIntervalValidation proves the extension_check_interval key
// accepts 0 (disabled) and >= 1h, and rejects positive values below 1h.
func TestExtensionCheckIntervalValidation(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	// 0 / "0s" = disabled; must be accepted and canonicalize to "0s".
	if err := svc.Set(ctx, settings.KeyExtensionCheckInterval, "0"); err != nil {
		t.Fatalf("Set 0: %v", err)
	}
	if got := svc.ExtensionCheckInterval(ctx); got != 0 {
		t.Errorf("ExtensionCheckInterval after Set 0 = %v, want 0", got)
	}

	// "0s" is also valid (canonical form).
	if err := svc.Set(ctx, settings.KeyExtensionCheckInterval, "0s"); err != nil {
		t.Fatalf("Set 0s: %v", err)
	}
	if got := svc.ExtensionCheckInterval(ctx); got != 0 {
		t.Errorf("ExtensionCheckInterval after Set 0s = %v, want 0", got)
	}

	// Below 1h (but non-zero) must be rejected.
	if err := svc.Set(ctx, settings.KeyExtensionCheckInterval, "30m"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set 30m: want ErrInvalidSetting, got %v", err)
	}

	// Exactly 1h must be accepted.
	if err := svc.Set(ctx, settings.KeyExtensionCheckInterval, "1h"); err != nil {
		t.Fatalf("Set 1h: %v", err)
	}
	if got := svc.ExtensionCheckInterval(ctx); got != time.Hour {
		t.Errorf("ExtensionCheckInterval after Set 1h = %v, want 1h", got)
	}

	// 24h must be accepted.
	if err := svc.Set(ctx, settings.KeyExtensionCheckInterval, "24h"); err != nil {
		t.Fatalf("Set 24h: %v", err)
	}
	if got := svc.ExtensionCheckInterval(ctx); got != 24*time.Hour {
		t.Errorf("ExtensionCheckInterval after Set 24h = %v, want 24h", got)
	}
}

// TestExtensionCheckIntervalDefaultAccessor proves ExtensionCheckInterval returns
// the config default (24h) when no DB override exists.
func TestExtensionCheckIntervalDefaultAccessor(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.ExtensionCheckInterval(ctx); got != 24*time.Hour {
		t.Errorf("ExtensionCheckInterval default = %v, want 24h", got)
	}
}

// TestWarmupInterval proves the warm-up interval accessor returns the default
// (15m) when unset, accepts 0 (disabled) and >= 1m, and rejects sub-1m values.
func TestWarmupInterval(t *testing.T) {
	assertMinOrZeroDurationTunable(t, settings.KeyWarmupInterval,
		func(s *settings.Service, ctx context.Context) time.Duration { return s.WarmupInterval(ctx) },
		15*time.Minute, "30s", 5*time.Minute)
}

// TestEngineSuperviseInterval proves the supervisor-interval accessor returns the
// default (30s) when unset, accepts 0 (disabled) and >= 5s, and rejects sub-5s
// values (GAP-114).
func TestEngineSuperviseInterval(t *testing.T) {
	assertMinOrZeroDurationTunable(t, settings.KeyEngineSuperviseInterval,
		func(s *settings.Service, ctx context.Context) time.Duration { return s.EngineSuperviseInterval(ctx) },
		30*time.Second, "1s", time.Minute)
}

// TestWarmupSlowThresholdMs proves the slow-threshold accessor returns the
// default (5000) when unset, accepts an in-bounds value, and rejects out-of-bounds.
func TestWarmupSlowThresholdMs(t *testing.T) {
	assertIntTunable(t, settings.KeyWarmupSlowThresholdMs,
		func(s *settings.Service, ctx context.Context) int { return s.WarmupSlowThresholdMs(ctx) },
		5000, 8000, 50, 600001)
}

// TestCacheTTLs proves the two interactive-cache TTL accessors return the default
// (1h) when unset, accept 0 (caching disabled) and >= 1s, reject sub-1s values,
// and reflect a valid override (the hot-reload contract the caches rely on).
func TestCacheTTLs(t *testing.T) {
	cases := []struct {
		name string
		key  string
		get  func(*settings.Service, context.Context) time.Duration
	}{
		{"search", settings.KeySearchCacheTTL,
			func(s *settings.Service, ctx context.Context) time.Duration { return s.SearchCacheTTL(ctx) }},
		{"chapter", settings.KeyChapterCacheTTL,
			func(s *settings.Service, ctx context.Context) time.Duration { return s.ChapterCacheTTL(ctx) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertMinOrZeroDurationTunable(t, c.key, c.get, time.Hour, "500ms", 2*time.Hour)
		})
	}
}

// TestDownloadConcurrency proves the PER-SOURCE download-concurrency accessor
// returns the default (5) when unset, rejects out-of-bounds values (below 1 /
// above 32), and reflects a valid override (the hot-reload contract the dispatcher
// relies on). The 32 ceiling bounds how hard ONE source is hit at once — the
// anti-ban throttle, which is why it is far tighter than the global cap below.
func TestDownloadConcurrency(t *testing.T) {
	assertIntTunable(t, settings.KeyDownloadConcurrency,
		func(s *settings.Service, ctx context.Context) int { return s.DownloadConcurrency(ctx) },
		5, 8, 0, 33)
}

// TestMaxConcurrentDownloads proves the GLOBAL download-concurrency accessor
// returns the default (6) when unset, rejects out-of-bounds values (below 1 /
// above 64), and reflects a valid override (the hot-reload contract the download
// cycle relies on to size its shared semaphore). The 64 ceiling bounds total
// in-flight fetches across EVERY source — it protects this host's own resources,
// not any single source, hence the wider bound.
func TestMaxConcurrentDownloads(t *testing.T) {
	assertIntTunable(t, settings.KeyMaxConcurrentDownloads,
		func(s *settings.Service, ctx context.Context) int { return s.MaxConcurrentDownloads(ctx) },
		6, 10, 0, 65)
}

// TestSourcesFailureThreshold proves the circuit-breaker trip-threshold accessor
// returns the default (5) when unset, rejects out-of-bounds values (below 1 /
// above the 100 sanity ceiling), and reflects a valid override (source-
// politeness Task 2).
func TestSourcesFailureThreshold(t *testing.T) {
	assertIntTunable(t, settings.KeySourcesFailureThreshold,
		func(s *settings.Service, ctx context.Context) int { return s.SourcesFailureThreshold(ctx) },
		5, 3, 0, 101)
}

// TestSourcesCooldown proves the circuit-breaker cooldown accessor returns the
// default (30m) when unset, rejects a value below 1m, and reflects a valid
// override.
func TestSourcesCooldown(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.SourcesCooldown(ctx); got != 30*time.Minute {
		t.Errorf("SourcesCooldown default = %v, want 30m", got)
	}
	if err := svc.Set(ctx, settings.KeySourcesCooldown, "30s"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set 30s (below 1m): want ErrInvalidSetting, got %v", err)
	}
	if err := svc.Set(ctx, settings.KeySourcesCooldown, "10m"); err != nil {
		t.Fatalf("Set 10m: %v", err)
	}
	if got := svc.SourcesCooldown(ctx); got != 10*time.Minute {
		t.Errorf("SourcesCooldown after Set = %v, want 10m", got)
	}
}

// TestSourcesMinRequestDelay_Default proves the politeness-delay accessor
// returns the config default (500ms) when the Settings table has no override.
func TestSourcesMinRequestDelay_Default(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())

	if got := svc.SourcesMinRequestDelay(context.Background()); got != 500*time.Millisecond {
		t.Errorf("SourcesMinRequestDelay default = %v, want 500ms", got)
	}
}

// TestSourcesMinRequestDelay_SetValidation is table-driven over the shapes the
// politeness delay must accept or reject: 0 (disabled), an arbitrary positive
// duration, and a rejected negative duration — proving the resolved accessor
// value matches what was stored for every accepted case.
func TestSourcesMinRequestDelay_SetValidation(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		want    time.Duration
	}{
		{name: "zero disables politeness", raw: "0", want: 0},
		{name: "arbitrary positive duration", raw: "1200ms", want: 1200 * time.Millisecond},
		{name: "negative duration rejected", raw: "-5s", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testdb.New(t)
			svc := settings.NewService(db, testDefaults())
			ctx := context.Background()

			err := svc.Set(ctx, settings.KeySourcesMinRequestDelay, tc.raw)
			if tc.wantErr {
				if !errors.Is(err, settings.ErrInvalidSetting) {
					t.Fatalf("Set(%q): want ErrInvalidSetting, got %v", tc.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q): %v", tc.raw, err)
			}
			if got := svc.SourcesMinRequestDelay(ctx); got != tc.want {
				t.Errorf("SourcesMinRequestDelay after Set(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestImageRequestDelayRuntimeOverride proves image pacing resolves the config
// default, permits zero to disable it, rejects negative durations, and reloads
// a valid database override without rebuilding the service.
func TestImageRequestDelayRuntimeOverride(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.ImageRequestDelay(ctx); got != 500*time.Millisecond {
		t.Fatalf("ImageRequestDelay default = %v, want 500ms", got)
	}
	if err := svc.Set(ctx, settings.KeySourcesImageRequestDelay, "0s"); err != nil {
		t.Fatalf("Set(0s): %v", err)
	}
	if got := svc.ImageRequestDelay(ctx); got != 0 {
		t.Fatalf("ImageRequestDelay after Set(0s) = %v, want 0", got)
	}
	if err := svc.Set(ctx, settings.KeySourcesImageRequestDelay, "-1s"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("Set(-1s): want ErrInvalidSetting, got %v", err)
	}
	if err := svc.Set(ctx, settings.KeySourcesImageRequestDelay, "1250ms"); err != nil {
		t.Fatalf("Set(1250ms): %v", err)
	}
	if got := svc.ImageRequestDelay(ctx); got != 1250*time.Millisecond {
		t.Errorf("ImageRequestDelay after Set(1250ms) = %v, want 1250ms", got)
	}
}

// TestSuppressSplitParts_DefaultAndOverride proves the fractional-part-
// suppression flag defaults to the injected value, round-trips through Set,
// and rejects a non-boolean value (fail-closed).
func TestSuppressSplitParts_DefaultAndOverride(t *testing.T) {
	assertBoolTunable(t, settings.KeySuppressSplitParts,
		func(s *settings.Service, ctx context.Context) bool { return s.SuppressSplitParts(ctx) },
		true)
}

// TestNotificationsEnabled_DefaultAndOverride proves the notifications.enabled
// tunable defaults to true, round-trips a false override, and rejects a
// non-boolean value (fail-closed) — mirroring the other bool tunables.
func TestNotificationsEnabled_DefaultAndOverride(t *testing.T) {
	assertBoolTunable(t, settings.KeyNotificationsEnabled,
		func(s *settings.Service, ctx context.Context) bool { return s.NotificationsEnabled(ctx) },
		true)
}

// TestMetadataAutoIdentify_DefaultAndOverride proves the metadata.auto_identify
// tunable defaults to true and round-trips a Set override, mirroring
// TestSuppressSplitParts_DefaultAndOverride for the other bool tunable.
func TestMetadataAutoIdentify_DefaultAndOverride(t *testing.T) {
	assertBoolTunable(t, settings.KeyMetadataAutoIdentify,
		func(s *settings.Service, ctx context.Context) bool { return s.MetadataAutoIdentify(ctx) },
		true)
}

// TestFlareSolverrDefaults proves every FlareSolverr accessor returns its
// injected default when the Settings table has no override (QCAT-238 —
// Tsundoku-owned Cloudflare-bypass config).
func TestFlareSolverrDefaults(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if svc.FlareSolverrEnabled(ctx) {
		t.Error("FlareSolverrEnabled default = true, want false")
	}
	if got := svc.FlareSolverrURL(ctx); got != "" {
		t.Errorf("FlareSolverrURL default = %q, want \"\"", got)
	}
	if got := svc.FlareSolverrTimeout(ctx); got != 60 {
		t.Errorf("FlareSolverrTimeout default = %d, want 60", got)
	}
	if got := svc.FlareSolverrSessionName(ctx); got != "" {
		t.Errorf("FlareSolverrSessionName default = %q, want \"\"", got)
	}
	if got := svc.FlareSolverrSessionTTL(ctx); got != 15 {
		t.Errorf("FlareSolverrSessionTTL default = %d, want 15", got)
	}
	if svc.FlareSolverrResponseFallback(ctx) {
		t.Error("FlareSolverrResponseFallback default = true, want false")
	}
}

// TestImpersonateDefaults proves both impersonate-gateway accessors return
// their injected default (off / blank) when no override row exists (GAP-111).
func TestImpersonateDefaults(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if svc.ImpersonateEnabled(ctx) {
		t.Error("ImpersonateEnabled default = true, want false")
	}
	if got := svc.ImpersonateURL(ctx); got != "" {
		t.Errorf("ImpersonateURL default = %q, want \"\"", got)
	}
}

// TestImpersonateSetAndResolve proves the enabled flag + URL round-trip,
// including clearing the URL back to blank (blank = disabled), and that a
// malformed URL is rejected (shares the FlareSolverr URL validation kernel).
func TestImpersonateSetAndResolve(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyImpersonateEnabled, "true"); err != nil {
		t.Fatalf("Set enabled: %v", err)
	}
	if !svc.ImpersonateEnabled(ctx) {
		t.Error("after Set true, ImpersonateEnabled = false")
	}
	if err := svc.Set(ctx, settings.KeyImpersonateURL, "http://impersonate-gateway:8788"); err != nil {
		t.Fatalf("Set url: %v", err)
	}
	if got := svc.ImpersonateURL(ctx); got != "http://impersonate-gateway:8788" {
		t.Errorf("ImpersonateURL after Set = %q, want http://impersonate-gateway:8788", got)
	}
	if err := svc.Set(ctx, settings.KeyImpersonateURL, ""); err != nil {
		t.Fatalf("Set url blank: %v", err)
	}
	if got := svc.ImpersonateURL(ctx); got != "" {
		t.Errorf("ImpersonateURL after Set \"\" = %q, want \"\"", got)
	}
	if err := svc.Set(ctx, settings.KeyImpersonateURL, "not-a-url"); !errors.Is(err, settings.ErrInvalidSetting) {
		t.Errorf("Set malformed url err = %v, want ErrInvalidSetting", err)
	}
}

// TestImpersonateSourcesDefaultEmpty proves the per-source gating set (GAP-131)
// defaults to EMPTY — the fail-safe that makes an unlisted source take the plain
// okhttp path, i.e. the pre-GAP-111 behaviour, even while the group is enabled.
func TestImpersonateSourcesDefaultEmpty(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if got := svc.ImpersonateSources(ctx); len(got) != 0 {
		t.Errorf("ImpersonateSources default = %v, want empty", got)
	}
}

// TestImpersonateSourcesSetAndResolve proves the gating set round-trips through
// the overlay as numeric source ids, is canonicalised (trimmed, de-duplicated,
// ascending) so the stored value is stable regardless of submission order, and
// clears back to empty.
func TestImpersonateSourcesSetAndResolve(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyImpersonateSources, " 1998416842837112832 , 42, 42 "); err != nil {
		t.Fatalf("Set sources: %v", err)
	}
	got := svc.ImpersonateSources(ctx)
	want := []int64{42, 1998416842837112832}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ImpersonateSources = %v, want %v (deduped + ascending)", got, want)
	}
	// The stored canonical form is the normalised one, not the raw submission.
	row := findSetting(t, svc.List(ctx), settings.KeyImpersonateSources)
	if row.Value != "42,1998416842837112832" {
		t.Errorf("stored value = %q, want %q", row.Value, "42,1998416842837112832")
	}

	if err := svc.Set(ctx, settings.KeyImpersonateSources, ""); err != nil {
		t.Fatalf("Set sources blank: %v", err)
	}
	if got := svc.ImpersonateSources(ctx); len(got) != 0 {
		t.Errorf("ImpersonateSources after clearing = %v, want empty", got)
	}
}

// TestImpersonateSourcesRejectsNonNumeric proves the set is fail-closed: a
// source NAME (or any non-numeric token) is rejected, so the owner-facing
// name→id mapping can never leak an id-shaped-as-name onto the engine wire —
// the GAP-120 drift class this boundary deliberately avoids.
func TestImpersonateSourcesRejectsNonNumeric(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	for _, raw := range []string{"Hive Scans", "42,Comix", "42,,43", "1e9", "-1"} {
		if err := svc.Set(ctx, settings.KeyImpersonateSources, raw); !errors.Is(err, settings.ErrInvalidSetting) {
			t.Errorf("Set %q err = %v, want ErrInvalidSetting", raw, err)
		}
	}
}

// TestFlareSolverrSetAndResolve_Enabled proves the enabled flag round-trips.
func TestFlareSolverrSetAndResolve_Enabled(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyFlareSolverrEnabled, "true"); err != nil {
		t.Fatalf("Set enabled: %v", err)
	}
	if !svc.FlareSolverrEnabled(ctx) {
		t.Error("after Set true, FlareSolverrEnabled = false")
	}
}

// TestFlareSolverrSetAndResolve_URL proves the URL round-trips, including
// clearing it back to blank (blank is always legal — "not configured").
func TestFlareSolverrSetAndResolve_URL(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyFlareSolverrURL, "http://flaresolverr:8191"); err != nil {
		t.Fatalf("Set url: %v", err)
	}
	if got := svc.FlareSolverrURL(ctx); got != "http://flaresolverr:8191" {
		t.Errorf("FlareSolverrURL after Set = %q, want http://flaresolverr:8191", got)
	}
	if err := svc.Set(ctx, settings.KeyFlareSolverrURL, ""); err != nil {
		t.Fatalf("Set url blank: %v", err)
	}
	if got := svc.FlareSolverrURL(ctx); got != "" {
		t.Errorf("FlareSolverrURL after Set \"\" = %q, want \"\"", got)
	}
}

// TestFlareSolverrSetAndResolve_TimeoutAndSession proves the timeout, session
// name (trimmed), and session TTL round-trip through their accessors.
func TestFlareSolverrSetAndResolve_TimeoutAndSession(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyFlareSolverrTimeout, "120"); err != nil {
		t.Fatalf("Set timeout: %v", err)
	}
	if got := svc.FlareSolverrTimeout(ctx); got != 120 {
		t.Errorf("FlareSolverrTimeout after Set = %d, want 120", got)
	}

	if err := svc.Set(ctx, settings.KeyFlareSolverrSessionName, "  tsundoku  "); err != nil {
		t.Fatalf("Set session name: %v", err)
	}
	if got := svc.FlareSolverrSessionName(ctx); got != "tsundoku" {
		t.Errorf("FlareSolverrSessionName after Set = %q, want trimmed \"tsundoku\"", got)
	}

	if err := svc.Set(ctx, settings.KeyFlareSolverrSessionTTL, "30"); err != nil {
		t.Fatalf("Set session ttl: %v", err)
	}
	if got := svc.FlareSolverrSessionTTL(ctx); got != 30 {
		t.Errorf("FlareSolverrSessionTTL after Set = %d, want 30", got)
	}
}

// TestFlareSolverrSetAndResolve_ResponseFallback proves the
// asResponseFallback mirror flag round-trips.
func TestFlareSolverrSetAndResolve_ResponseFallback(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyFlareSolverrResponseFallback, "true"); err != nil {
		t.Fatalf("Set response fallback: %v", err)
	}
	if !svc.FlareSolverrResponseFallback(ctx) {
		t.Error("after Set true, FlareSolverrResponseFallback = false")
	}
}

// TestFlareSolverrURLValidation proves the URL tunable accepts blank or a
// well-formed absolute http(s) URL and rejects everything else (a relative
// path, a non-http(s) scheme, or a hostless URL).
func TestFlareSolverrURLValidation(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"blank clears", "", false},
		{"valid http", "http://flaresolverr:8191", false},
		{"valid https", "https://flaresolverr.example.com", false},
		{"relative path rejected", "/flaresolverr", true},
		{"non-http scheme rejected", "ftp://flaresolverr:8191", true},
		{"hostless rejected", "http://", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Set(ctx, settings.KeyFlareSolverrURL, tc.raw)
			if tc.wantErr {
				if !errors.Is(err, settings.ErrInvalidSetting) {
					t.Fatalf("Set(%q): want ErrInvalidSetting, got %v", tc.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q): %v", tc.raw, err)
			}
		})
	}
}

// TestFlareSolverrIntBounds proves the timeout (5..600s) and session-ttl
// (0..1440m) tunables reject out-of-bounds values.
func TestFlareSolverrIntBounds(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	cases := []struct {
		name, key, value string
	}{
		{"timeout below min", settings.KeyFlareSolverrTimeout, "4"},
		{"timeout above max", settings.KeyFlareSolverrTimeout, "601"},
		{"timeout unparseable", settings.KeyFlareSolverrTimeout, "soon"},
		{"session ttl negative", settings.KeyFlareSolverrSessionTTL, "-1"},
		{"session ttl above max", settings.KeyFlareSolverrSessionTTL, "1441"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.Set(ctx, tc.key, tc.value); !errors.Is(err, settings.ErrInvalidSetting) {
				t.Fatalf("Set(%q, %q): want ErrInvalidSetting, got %v", tc.key, tc.value, err)
			}
		})
	}

	// Bounds edges are accepted.
	if err := svc.Set(ctx, settings.KeyFlareSolverrTimeout, "5"); err != nil {
		t.Fatalf("Set timeout=5 (min): %v", err)
	}
	if err := svc.Set(ctx, settings.KeyFlareSolverrTimeout, "600"); err != nil {
		t.Fatalf("Set timeout=600 (max): %v", err)
	}
	if err := svc.Set(ctx, settings.KeyFlareSolverrSessionTTL, "0"); err != nil {
		t.Fatalf("Set sessionTtl=0 (min): %v", err)
	}
	if err := svc.Set(ctx, settings.KeyFlareSolverrSessionTTL, "1440"); err != nil {
		t.Fatalf("Set sessionTtl=1440 (max): %v", err)
	}
}

// TestEngineSocksDefaults proves every engine.socks_* accessor returns its
// injected default when the Settings table has no override.
func TestEngineSocksDefaults(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if svc.EngineSocksEnabled(ctx) {
		t.Error("EngineSocksEnabled default = true, want false")
	}
	if got := svc.EngineSocksHost(ctx); got != "" {
		t.Errorf("EngineSocksHost default = %q, want \"\"", got)
	}
	if got := svc.EngineSocksPort(ctx); got != 1080 {
		t.Errorf("EngineSocksPort default = %d, want 1080", got)
	}
	if got := svc.EngineSocksVersion(ctx); got != 5 {
		t.Errorf("EngineSocksVersion default = %d, want 5", got)
	}
}

// TestEngineSocksSetAndResolve proves every engine.socks_* tunable round-trips
// through Set + its typed accessor.
func TestEngineSocksSetAndResolve(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	if err := svc.Set(ctx, settings.KeyEngineSocksEnabled, "true"); err != nil {
		t.Fatalf("Set enabled: %v", err)
	}
	if !svc.EngineSocksEnabled(ctx) {
		t.Error("after Set true, EngineSocksEnabled = false")
	}

	if err := svc.Set(ctx, settings.KeyEngineSocksHost, "  socks.internal  "); err != nil {
		t.Fatalf("Set host: %v", err)
	}
	if got := svc.EngineSocksHost(ctx); got != "socks.internal" {
		t.Errorf("EngineSocksHost after Set = %q, want trimmed \"socks.internal\"", got)
	}

	if err := svc.Set(ctx, settings.KeyEngineSocksPort, "1081"); err != nil {
		t.Fatalf("Set port: %v", err)
	}
	if got := svc.EngineSocksPort(ctx); got != 1081 {
		t.Errorf("EngineSocksPort after Set = %d, want 1081", got)
	}

	if err := svc.Set(ctx, settings.KeyEngineSocksVersion, "4"); err != nil {
		t.Fatalf("Set version=4: %v", err)
	}
	if got := svc.EngineSocksVersion(ctx); got != 4 {
		t.Errorf("EngineSocksVersion after Set = %d, want 4", got)
	}
}

// TestEngineSocksPortBounds proves the port tunable rejects values outside
// [1, 65535].
func TestEngineSocksPortBounds(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	cases := []struct{ name, value string }{
		{"below min", "0"},
		{"above max", "65536"},
		{"unparseable", "many"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.Set(ctx, settings.KeyEngineSocksPort, tc.value); !errors.Is(err, settings.ErrInvalidSetting) {
				t.Fatalf("Set(port, %q): want ErrInvalidSetting, got %v", tc.value, err)
			}
		})
	}

	if err := svc.Set(ctx, settings.KeyEngineSocksPort, "1"); err != nil {
		t.Fatalf("Set port=1 (min): %v", err)
	}
	if err := svc.Set(ctx, settings.KeyEngineSocksPort, "65535"); err != nil {
		t.Fatalf("Set port=65535 (max): %v", err)
	}
}

// TestEngineSocksVersionMustBe4Or5 proves the version tunable accepts ONLY 4
// or 5 — not a contiguous range like every other bounded int tunable.
func TestEngineSocksVersionMustBe4Or5(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	cases := []struct{ name, value string }{
		{"zero", "0"},
		{"three", "3"},
		{"six", "6"},
		{"unparseable", "five"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.Set(ctx, settings.KeyEngineSocksVersion, tc.value); !errors.Is(err, settings.ErrInvalidSetting) {
				t.Fatalf("Set(version, %q): want ErrInvalidSetting, got %v", tc.value, err)
			}
		})
	}

	if err := svc.Set(ctx, settings.KeyEngineSocksVersion, "4"); err != nil {
		t.Fatalf("Set version=4: %v", err)
	}
	if err := svc.Set(ctx, settings.KeyEngineSocksVersion, "5"); err != nil {
		t.Fatalf("Set version=5: %v", err)
	}
}

// TestExistingKeys proves the gap-detection reader: it returns exactly the
// queried keys that already have an explicit Settings row (owned by Tsundoku),
// omits keys that are still unset (resolving to their default), never reports a
// key that was not asked about, and short-circuits an empty query.
func TestExistingKeys(t *testing.T) {
	db := testdb.New(t)
	svc := settings.NewService(db, testDefaults())
	ctx := context.Background()

	// Give two keys explicit rows; a third is left unset.
	if err := svc.Set(ctx, settings.KeyFlareSolverrURL, "http://fs.example:8191"); err != nil {
		t.Fatalf("Set url: %v", err)
	}
	if err := svc.Set(ctx, settings.KeyFlareSolverrTimeout, "90"); err != nil {
		t.Fatalf("Set timeout: %v", err)
	}

	got, err := svc.ExistingKeys(ctx, []string{
		settings.KeyFlareSolverrURL,
		settings.KeyFlareSolverrTimeout,
		settings.KeyFlareSolverrEnabled, // unset → must be absent
	})
	if err != nil {
		t.Fatalf("ExistingKeys: %v", err)
	}
	if !got[settings.KeyFlareSolverrURL] {
		t.Error("KeyFlareSolverrURL missing from ExistingKeys, want present (it has a row)")
	}
	if !got[settings.KeyFlareSolverrTimeout] {
		t.Error("KeyFlareSolverrTimeout missing from ExistingKeys, want present (it has a row)")
	}
	if got[settings.KeyFlareSolverrEnabled] {
		t.Error("KeyFlareSolverrEnabled present in ExistingKeys, want absent (it has no row)")
	}

	// An empty query short-circuits to an empty, non-nil set with no error.
	empty, err := svc.ExistingKeys(ctx, nil)
	if err != nil {
		t.Fatalf("ExistingKeys(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("ExistingKeys(nil) = %v, want empty", empty)
	}
}
