// Package download_test — end-to-end broken-image reliability proof.
//
// This is the dispatcher-level guarantee behind the owner's "never save a chapter
// with a missing/broken panel" invariant: with the REAL, validating
// sourceengine.Fetcher wired in, a multi-page chapter whose middle page is a
// truncated image fails the WHOLE attempt cleanly — the chapter never reaches
// downloaded, NO CBZ is written, and the source is bumped so the existing
// per-source retry drives a later attempt.
//
// Ported from the retired suwayomi-era internal/download/brokenimage_test.go
// (GAP-083) onto the P2 engine-host Fetcher — same guarantee, different
// transport.
// Requires Docker (via testcontainers).
package download_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sse"
)

// brokenPageClient is a minimal sourceengine.Client that serves a fixed page
// list, one of whose pages is broken. It embeds the Client interface (nil) so
// only the two methods the Fetcher calls need real bodies; any other method
// would panic, proving the Fetcher touches nothing else.
type brokenPageClient struct {
	sourceengine.Client
	pages    []sourceengine.Page
	pageData map[string][]byte
}

func (b *brokenPageClient) Pages(_ context.Context, _ int64, _ string) ([]sourceengine.Page, error) {
	return b.pages, nil
}

func (b *brokenPageClient) Image(_ context.Context, _ int64, pageURL, _ string) ([]byte, string, error) {
	return b.pageData[pageURL], "image/jpeg", nil
}

// encodeTestJPEG returns a real, fully-decodable 2x2 JPEG.
func encodeTestJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 10, G: 200, B: 30, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode test JPEG: %v", err)
	}
	return buf.Bytes()
}

// TestRunOnce_BrokenPage_ChapterFailsNoCBZ wires the real validating Fetcher and a
// client whose second page is a truncated JPEG. After one RunOnce pass the chapter
// must NOT be downloaded, no .cbz may exist under storage, the chapter must carry no
// filename, and the source's per-source retry state must be bumped (so it retries).
func TestRunOnce_BrokenPage_ChapterFailsNoCBZ(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := mustTempDir(t)

	s := client.Series.Create().SetTitle("Broken Panel").SetSlug("broken-panel").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("7").SetImportance(10).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("c1").
		SetURL("/ch/c1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SaveX(ctx)

	good := encodeTestJPEG(t)
	broken := good[:12] // valid magic, truncated body → fails full decode
	bc := &brokenPageClient{
		pages: []sourceengine.Page{
			{Index: 0, URL: "u0"},
			{Index: 1, URL: "u1"}, // the broken middle page
			{Index: 2, URL: "u2"},
		},
		pageData: map[string][]byte{
			"u0": good,
			"u1": broken,
			"u2": good,
		},
	}

	d := download.New(client, sourceengine.NewFetcher(bc), sse.NewHub(),
		download.Config{Storage: storage}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: unexpected error: %v", err)
	}

	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State == entchapter.StateDownloaded {
		t.Errorf("chapter state = downloaded, want a failure state (broken page must not save)")
	}
	if got.State != entchapter.StateFailed {
		t.Errorf("chapter state = %s, want failed (one source, attempts<maxRetries ⇒ retriable)", got.State)
	}
	if got.Filename != "" {
		t.Errorf("chapter filename = %q, want empty (no CBZ written)", got.Filename)
	}

	// No CBZ may exist anywhere under storage — not even a partial one.
	var cbz []string
	_ = filepath.WalkDir(storage, func(path string, _ os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(path, ".cbz") {
			cbz = append(cbz, path)
		}
		return nil
	})
	if len(cbz) != 0 {
		t.Errorf("found CBZ files %v, want none (broken chapter must write no file)", cbz)
	}

	// The source is bumped so the chapter retries on a later cycle.
	if got := client.ProviderChapter.GetX(ctx, pc.ID); got.Attempts != 1 {
		t.Errorf("ProviderChapter.attempts = %d, want 1 (source bumped for retry)", got.Attempts)
	}
}
