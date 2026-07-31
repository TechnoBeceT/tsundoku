// Package download_test — the DEFERRED failure axis (GAP-141): a chapter the
// source is deliberately withholding behind coins / early access.
package download_test

import (
	"context"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// lockedSettings makes the deferral horizon DISTINGUISHABLE from the ordinary
// retry backoff. That separation is the whole point: deferSource and
// cooldownSource differ in exactly one observable — the interval — so a test that
// left them equal could not tell the two apart, and deleting the deferred branch
// would keep passing.
func lockedSettings() settings.Static {
	return settings.Static{
		Retries: 3, Backoff: 30 * time.Minute, LockedRetry: 72 * time.Hour, DownloadConc: 1,
		SourcesFailureThresh: 1, SourcesCooldownIv: time.Hour, SourcesMinDelay: 0,
	}
}

// lockedUpstreamError is the exact shape engine-host produces for a paywalled
// chapter: every extension exception is wrapped in a 502 envelope.
func lockedUpstreamError() error {
	return &sourceengine.UpstreamError{Status: 502, Msg: "Exception: Chapter locked (coins required)"}
}

// TestFetchFailure_Locked_DefersWithoutChargingOrTripping proves all THREE halves
// of the deferred axis at once, against a real database.
//
// Hive Scans withholds its newest chapters behind coins for a few days and then
// releases them free, so the chapter is not faulty and neither is the source:
//   - attempts must stay 0 — charging them would exhaust the budget and reach
//     permanently_failed DAYS before the chapter goes free, after which nothing
//     retries it;
//   - the breaker must stay closed — one paid chapter must not pause a source that
//     is serving everything else (36 such errors in 6h had left Hive in a cooldown
//     loop for 3+ hours);
//   - next_attempt_at must be the LOCKED horizon, not the retry backoff — this is
//     the assertion that fails if the deferred branch is ever removed and locked
//     failures fall through to cooldownSource.
func TestFetchFailure_Locked_DefersWithoutChargingOrTripping(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	_, pc := singleSourceChapter(ctx, t, client) // provider "mangadex" ⇒ breaker key "mangadex"

	rs := lockedSettings()
	gate := sourcegate.NewService(client, rs)
	f := fake.New(fake.WithError(lockedUpstreamError()))
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, rs, gate)

	before := time.Now().UTC()
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.ProviderChapter.GetX(ctx, pc.ID)
	if got.Attempts != 0 {
		t.Errorf("attempts = %d, want 0 (a withheld chapter must never spend its budget)", got.Attempts)
	}
	if got.NextAttemptAt == nil {
		t.Fatal("next_attempt_at = nil, want the locked horizon")
	}

	// The horizon must land near now+72h, and — crucially — NOT near the 30m
	// backoff a cooldown would have written. A generous window still separates the
	// two by orders of magnitude, so this stays robust without being vacuous.
	delta := got.NextAttemptAt.Sub(before)
	if delta < 71*time.Hour || delta > 73*time.Hour {
		t.Errorf("next_attempt_at is %v out, want ~72h (the locked horizon, NOT the %v retry backoff)",
			delta, rs.Backoff)
	}

	if !gate.IsAvailable(ctx, "mangadex", time.Now().UTC()) {
		t.Error("source breaker tripped on a locked chapter — the source is healthy and must stay available")
	}
}

// TestLockedHorizonClampsAwayZero pins the floor that is the ONLY backstop on this
// axis. The settings validator's >= 1h bound guards an owner-set override, but the
// env-injected default and settings.Static's zero value both reach the dispatcher
// unchecked. A zero interval sets next_attempt_at to `now`, and because a deferral
// burns no attempts and trips no breaker there is nothing else to stop it — the
// same chapter would be re-fetched every cycle, forever and invisibly.
func TestLockedHorizonClampsAwayZero(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	_, pc := singleSourceChapter(ctx, t, client)

	rs := lockedSettings()
	rs.LockedRetry = 0 // an unset default, as settings.Static{} would produce
	gate := sourcegate.NewService(client, rs)
	f := fake.New(fake.WithError(lockedUpstreamError()))
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, rs, gate)

	before := time.Now().UTC()
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.ProviderChapter.GetX(ctx, pc.ID)
	if got.NextAttemptAt == nil {
		t.Fatal("next_attempt_at = nil, want the clamped floor")
	}
	// Tolerance, not sloppiness: `before` is taken outside RunOnce and Postgres
	// stores the instant at microsecond precision, so the round-trip can land a few
	// hundred nanoseconds under a full hour. The property under test is "clamped to
	// the floor rather than left at zero" — a regression writes `now`, which is an
	// hour away from this bound, not a rounding tick.
	if delta := got.NextAttemptAt.Sub(before); delta < 59*time.Minute {
		t.Errorf("next_attempt_at is only %v out on a zero interval, want ~1h — "+
			"an unclamped deferral re-fetches every cycle with no exhaustion backstop", delta)
	}
}
