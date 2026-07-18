package library_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// fakeSourceLister is a minimal library.SourceLister: it reports the given
// numeric source ids as the engine host's loaded sources (or a Sources() error),
// independent of the ingest client, so the AddProvider/MatchDiskProvider
// membership check can be driven in isolation.
type fakeSourceLister struct {
	ids []int64
	err error
}

func (f fakeSourceLister) Sources(_ context.Context) ([]sourceengine.Source, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]sourceengine.Source, len(f.ids))
	for i, id := range f.ids {
		out[i] = sourceengine.Source{ID: id}
	}
	return out, nil
}

// TestAddProvider_MembershipMiss_ReturnsNotFound proves ErrSourceNotFound (→404)
// is returned ONLY on a TRUE membership miss: the engine host loads source id 2,
// but the owner requests "1", so the id is genuinely absent from Sources() and
// the ingest is never attempted.
func TestAddProvider_MembershipMiss_ReturnsNotFound(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	ser := client.Series.Create().SetTitle("Fresh").SetSlug("fresh").SaveX(ctx)

	ingestSvc := ingest.NewIngest(newFakeClientWithFeed(t), client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub()).
		WithSourceLister(fakeSourceLister{ids: []int64{2}})

	if _, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/1", 5, ""); !errors.Is(err, library.ErrSourceNotFound) {
		t.Fatalf("want ErrSourceNotFound on a true membership miss, got %v", err)
	}
}

// TestAddProvider_UpstreamFailure_ReturnsUpstream is the core regression proof
// for the phantom-404 bug: a genuine engine-host fetch failure (the source IS a
// loaded source — membership passes) must surface as ErrSourceUpstream (→502),
// NEVER the old blanket ErrSourceNotFound (→404).
func TestAddProvider_UpstreamFailure_ReturnsUpstream(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()
	ser := client.Series.Create().SetTitle("Fresh").SetSlug("fresh").SaveX(ctx)

	fake := &fakeAddProviderClient{chaptersErr: errors.New("engine host 502: page fetch failed")}
	ingestSvc := ingest.NewIngest(fake, client)
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub()).
		WithSourceLister(fakeSourceLister{ids: []int64{1}})

	_, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/1", 5, "")
	if !errors.Is(err, library.ErrSourceUpstream) {
		t.Fatalf("want ErrSourceUpstream on an engine-host fetch failure, got %v", err)
	}
	if errors.Is(err, library.ErrSourceNotFound) {
		t.Fatalf("must NOT be ErrSourceNotFound (the phantom-404 bug), got %v", err)
	}
}

// TestAddProvider_UngatedBypassesTrippedBreaker proves the anti-ban split: the
// GATED path (refresh sweep + download dispatcher) is refused by a tripped
// circuit-breaker, while the one-shot OWNER attach (AddProvider → AddSeriesUngated)
// bypasses the cooldown and succeeds. This is the fix for the confirmed prod
// symptom (Asura tripped by unrelated background failures blocked the owner's
// explicit Match/Add click).
func TestAddProvider_UngatedBypassesTrippedBreaker(t *testing.T) {
	storage := t.TempDir()
	writeKaizokuSeries(t, storage, "Manga", "My Series", "mangadex", "Alpha", 2)
	client := testdb.New(t)
	ctx := context.Background()

	facts, err := diskScanFirst(t, storage)
	if err != nil {
		t.Fatalf("diskScanFirst: %v", err)
	}
	importOneFromFacts(t, client, facts)
	ser := client.Series.Query().OnlyX(ctx)

	// The fake reports source 1 as "Weeb Source" — the physical-source gate key.
	fake := newFakeClientWithSearch(t, "My Series")
	gate := sourcegate.NewService(client, settings.Static{SourcesFailureThresh: 1, SourcesCooldownIv: time.Hour})

	// Trip the breaker for the physical source (threshold 1 → one failure trips).
	const key = "Weeb Source"
	now := time.Now()
	gate.RecordFailure(ctx, key, errors.New("cloudflare block"), now)
	if gate.IsAvailable(ctx, key, now) {
		t.Fatal("precondition: breaker should be tripped after 1 failure (threshold 1)")
	}

	ingestSvc := ingest.NewIngestWithGate(fake, client, nil, gate)

	// 1. The GATED path (what the sweep/dispatcher use) MUST stay refused.
	if _, err := ingestSvc.AddSeries(ctx, 1, "/manga/99", "My Series", ""); !errors.Is(err, ingest.ErrSourceCooledDown) {
		t.Fatalf("gated AddSeries must be refused by the tripped breaker, got %v", err)
	}
	if gate.IsAvailable(ctx, key, now) {
		t.Fatal("breaker must still be tripped after the refused gated fetch (nothing was fetched)")
	}

	// 2. The OWNER attach (ungated) SUCCEEDS despite the tripped breaker.
	seriesSvc := series.NewService(client, storage, 14)
	svc := library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub()).
		WithSourceLister(fake)
	dto, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 5, "")
	if err != nil {
		t.Fatalf("owner AddProvider must bypass the tripped breaker, got %v", err)
	}
	if len(dto.Providers) != 2 {
		t.Fatalf("providers = %d, want 2 (disk + weeb)", len(dto.Providers))
	}
}

// TestClassifyAttachError pins the honest error taxonomy in isolation: a
// cooled-down ingest error maps to ErrSourceUnavailable (503) and any other
// upstream failure to ErrSourceUpstream (502) — never ErrSourceNotFound.
func TestClassifyAttachError(t *testing.T) {
	cooled := library.ClassifyAttachError("12345", fmt.Errorf("%w: Weeb Source", ingest.ErrSourceCooledDown))
	if !errors.Is(cooled, library.ErrSourceUnavailable) {
		t.Errorf("cooled-down: want ErrSourceUnavailable, got %v", cooled)
	}
	if errors.Is(cooled, library.ErrSourceUpstream) || errors.Is(cooled, library.ErrSourceNotFound) {
		t.Errorf("cooled-down must be ONLY ErrSourceUnavailable, got %v", cooled)
	}

	upstream := library.ClassifyAttachError("12345", errors.New("engine host 502"))
	if !errors.Is(upstream, library.ErrSourceUpstream) {
		t.Errorf("generic: want ErrSourceUpstream, got %v", upstream)
	}
	if errors.Is(upstream, library.ErrSourceUnavailable) || errors.Is(upstream, library.ErrSourceNotFound) {
		t.Errorf("generic must be ONLY ErrSourceUpstream, got %v", upstream)
	}
}
