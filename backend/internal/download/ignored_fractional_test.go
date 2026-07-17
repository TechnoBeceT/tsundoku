// Package download_test — proves the dispatcher parks an all-carriers-ignored
// wanted/failed FRACTIONAL chapter in the terminal `ignored` state instead of
// leaving it stranded as "Wanted" forever (handleSourcelessChapter). This is the
// engine-level catch that stops such chapters re-accumulating between the
// series.SetIgnoreFractional toggle sweeps. Requires Docker (testcontainers).
package download_test

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// ignoreFracSeq disambiguates the slug across repeated seedings in one test run.
var ignoreFracSeq atomic.Int64

// seedFractionalOnSource seeds a NEW series with one wanted fractional chapter
// (number 1.5) offered by a single SeriesProvider whose ignore_fractional flag is
// `ignore`. Returns the chapter.
func seedFractionalOnSource(ctx context.Context, t *testing.T, client *ent.Client, ignore bool) *ent.Chapter {
	t.Helper()
	seq := ignoreFracSeq.Add(1)
	slug := "ignore-frac-" + strconv.FormatInt(seq, 10)
	s := client.Series.Create().SetTitle("Ignore Frac " + slug).SetSlug(slug).SaveX(ctx)
	sp := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("Comix").SetImportance(5).SetIgnoreFractional(ignore).SaveX(ctx)

	const key = "1.5"
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey(key).SetNumber(1.5).SetURL("https://x/" + key).SaveX(ctx)
	return client.Chapter.Create().
		SetSeries(s).SetChapterKey(key).SetNumber(1.5).SetState(entchapter.StateWanted).SaveX(ctx)
}

// noFetchFetcher fails every call and counts them — a parked chapter must never be
// fetched.
type noFetchFetcher struct{ calls atomic.Int64 }

func (f *noFetchFetcher) Fetch(context.Context, fetcher.FetchRef) (fetcher.ChapterPages, error) {
	f.calls.Add(1)
	return fetcher.ChapterPages{}, errors.New("simulated fetch failure")
}

func stateOf(ctx context.Context, t *testing.T, client *ent.Client, id uuid.UUID) entchapter.State {
	t.Helper()
	return client.Chapter.GetX(ctx, id).State
}

// TestRunOnce_ParksAllIgnoredFractional proves a wanted fractional whose ONLY
// carrier is a source flagged ignore_fractional is moved to `ignored` by a
// download pass — never fetched, and no longer clogging the wanted queue.
func TestRunOnce_ParksAllIgnoredFractional(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ch := seedFractionalOnSource(ctx, t, client, true)

	f := &noFetchFetcher{}
	rs := settings.Static{Retries: 3, Backoff: 0, DownloadConc: 1}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: t.TempDir()}, rs, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := f.calls.Load(); got != 0 {
		t.Errorf("fetch calls = %d, want 0 (an ignored fractional is never fetched)", got)
	}
	if got := stateOf(ctx, t, client, ch.ID); got != entchapter.StateIgnored {
		t.Errorf("chapter state = %s, want ignored", got)
	}
}

// TestRunOnce_NonIgnoredFractionalStaysDownloadable is the RESURRECTION GUARD at
// the engine level: a fractional whose carrier does NOT ignore fractionals is a
// live candidate — it is fetched, never parked. (The fetch fails here, so it lands
// in failed/permanently_failed, but it was ATTEMPTED, which ignored never is.)
func TestRunOnce_NonIgnoredFractionalStaysDownloadable(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ch := seedFractionalOnSource(ctx, t, client, false)

	f := &noFetchFetcher{}
	rs := settings.Static{Retries: 3, Backoff: 0, DownloadConc: 1}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: t.TempDir()}, rs, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := f.calls.Load(); got == 0 {
		t.Error("fetch calls = 0, want >=1 (a non-ignored fractional must be attempted, not parked)")
	}
	if got := stateOf(ctx, t, client, ch.ID); got == entchapter.StateIgnored {
		t.Error("chapter state = ignored, want a download outcome (never parked while a live source carries it)")
	}
}
