// Package reporting_test exercises the Source Health Console read/aggregation
// service end-to-end against an ephemeral PostgreSQL instance (testdb): it seeds
// SourceEvent rows (plus the metrics + breaker side-tables) and asserts the four
// reporting views aggregate correctly, that the aggregation is done in SQL (not
// by loading the whole log), and that period-window boundaries are exact. Tests
// require Docker.
package reporting_test

import (
	"context"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/reporting"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// newClient stands up a fresh ephemeral database and returns its ent client.
func newClient(t *testing.T) *ent.Client {
	t.Helper()
	return testdb.New(t)
}

// refNow is the fixed "now" every test passes to the service, so period windows
// and seeded event timestamps are fully deterministic (no dependency on the wall
// clock). Anchored at midday UTC so hour/day truncation never straddles a
// boundary for the offsets the tests use.
var refNow = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

// newService builds a reporting.Service over a fresh testdb, plus its collaborator
// services (metrics + breaker gate), and returns the raw ent client so a test can
// seed rows directly.
func newService(t *testing.T) (*reporting.Service, *ent.Client) {
	t.Helper()
	client := newClient(t)
	metricsSvc := metrics.NewService(client)
	gate := sourcegate.NewService(client, settings.Static{SourcesFailureThresh: 3, SourcesCooldownIv: 10 * time.Minute})
	return reporting.NewService(client, metricsSvc, gate), client
}

// ev is a compact SourceEvent seed spec. Only key/typ/stat/at are usually set;
// the rest default to zero (SourceID/name fall back to key, no error, no items).
type ev struct {
	key    string
	id     string
	name   string
	lang   string
	typ    entsourceevent.EventType
	stat   entsourceevent.Status
	at     time.Time
	errCat string
	errMsg string
	items  *int
	dur    time.Duration
	meta   map[string]string
}

// seed inserts one SourceEvent row from an ev spec. A blank name defaults to the
// key (the realistic case — source_key IS the canonical name).
func seed(t *testing.T, client *ent.Client, e ev) {
	t.Helper()
	name := e.name
	if name == "" {
		name = e.key
	}
	c := client.SourceEvent.Create().
		SetSourceKey(e.key).
		SetSourceID(e.id).
		SetSourceName(name).
		SetLanguage(e.lang).
		SetEventType(e.typ).
		SetStatus(e.stat).
		SetDurationMs(e.dur.Milliseconds()).
		SetCreatedAt(e.at)
	if e.errMsg != "" {
		c.SetErrorMessage(e.errMsg)
	}
	if e.errCat != "" {
		c.SetErrorCategory(e.errCat)
	}
	if e.items != nil {
		c.SetItemsCount(*e.items)
	}
	if e.meta != nil {
		c.SetMetadata(e.meta)
	}
	if err := c.Exec(context.Background()); err != nil {
		t.Fatalf("seed event %q: %v", e.key, err)
	}
}

// seedMetric inserts a rolling metric snapshot row (for the latency join).
func seedMetric(t *testing.T, client *ent.Client, sourceID, sourceName string, ewmaMs int) {
	t.Helper()
	if err := client.SourceMetric.Create().
		SetSourceID(sourceID).
		SetSourceName(sourceName).
		SetEwmaLatencyMs(ewmaMs).
		SetLastLatencyMs(ewmaMs).
		Exec(context.Background()); err != nil {
		t.Fatalf("seed metric %q: %v", sourceName, err)
	}
}

// tripBreaker inserts a tripped circuit-breaker row keyed by source name, with a
// failure streak that began at failingSince and a cooldown at cooldownUntil.
func tripBreaker(t *testing.T, client *ent.Client, sourceKey string, failingSince, cooldownUntil time.Time) {
	t.Helper()
	if err := client.SourceCircuitState.Create().
		SetSourceKey(sourceKey).
		SetConsecutiveFailures(3).
		SetLastError("cloudflare block").
		SetFailingSince(failingSince).
		SetCooldownUntil(cooldownUntil).
		Exec(context.Background()); err != nil {
		t.Fatalf("trip breaker %q: %v", sourceKey, err)
	}
}

// intptr is a small helper for the optional items_count seed field.
func intptr(n int) *int { return &n }

// assertEq fails the test when got != want, naming the field. It keeps the test
// bodies flat (one call per assertion) so their cyclomatic complexity stays under
// the lint cap even with many checks.
func assertEq[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// assertTrue fails the test when cond is false, with the given message.
func assertTrue(t *testing.T, msg string, cond bool) {
	t.Helper()
	if !cond {
		t.Error(msg)
	}
}
