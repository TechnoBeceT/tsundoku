package download_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
	"github.com/technobecet/tsundoku/internal/sse"
)

// throughputIntegrationClient exposes request starts at the engine boundary. It
// holds the first wave of page-list calls until the expected asymmetric
// dispatcher admission (one overridden + two inherited) is observable, then
// records image starts and injects one transient retry for the overridden source.
type throughputIntegrationClient struct {
	*enginefake.Client

	mu            sync.Mutex
	pageInFlight  map[int64]int
	pageMax       map[int64]int
	pageStarted   chan struct{}
	releasePages  chan struct{}
	imageStarts   map[int64][]time.Time
	retryInjected bool
}

func (c *throughputIntegrationClient) Pages(ctx context.Context, sourceID int64, chapterURL, mangaURL string) ([]sourceengine.Page, error) {
	c.mu.Lock()
	c.pageInFlight[sourceID]++
	c.pageMax[sourceID] = max(c.pageMax[sourceID], c.pageInFlight[sourceID])
	c.mu.Unlock()
	c.pageStarted <- struct{}{}
	select {
	case <-c.releasePages:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	c.mu.Lock()
	c.pageInFlight[sourceID]--
	c.mu.Unlock()
	return c.Client.Pages(ctx, sourceID, chapterURL, mangaURL)
}

func (c *throughputIntegrationClient) Image(ctx context.Context, sourceID int64, pageURL, imageURL string) ([]byte, string, error) {
	c.mu.Lock()
	c.imageStarts[sourceID] = append(c.imageStarts[sourceID], time.Now())
	if sourceID == 101 && !c.retryInjected {
		c.retryInjected = true
		c.mu.Unlock()
		return nil, "", errors.New("502 bad gateway")
	}
	c.mu.Unlock()
	return c.Client.Image(ctx, sourceID, pageURL, imageURL)
}

func TestStoredSourceThroughputPolicyDrivesDispatcherAndImagePacer(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	limitedIDs := seedSourceChapters(ctx, t, client, "limited-integration", "101", 10, 2)
	inheritedIDs := seedSourceChapters(ctx, t, client, "inherited-integration", "202", 10, 2)

	defaults := settings.Static{DownloadConc: 2, Retries: 3, Backoff: time.Hour, ImageRequestDelayIv: 0}
	policies := sourcethroughput.NewService(client, defaults)
	if _, err := policies.Update(ctx, 101, sourcethroughput.Patch{
		DownloadConcurrency: sourcethroughput.Set(1),
		ImageRequestDelay:   sourcethroughput.Set(750 * time.Millisecond),
	}); err != nil {
		t.Fatalf("store source throughput override: %v", err)
	}

	jpg := integrationJPEG(t)
	var opts []enginefake.Option
	for _, sourceID := range []int64{101, 202} {
		for chapter := 1; chapter <= 2; chapter++ {
			chapterURL := "https://" + strconv.FormatInt(sourceID, 10) + "/" + itoa(chapter)
			pageURL := chapterURL + "/page"
			opts = append(opts,
				enginefake.WithPages(sourceID, chapterURL, []sourceengine.Page{{Index: 0, URL: pageURL}}),
				enginefake.WithImage(sourceID, pageURL, jpg, "image/jpeg"),
			)
		}
	}
	engine := &throughputIntegrationClient{
		Client:       enginefake.New(opts...),
		pageInFlight: map[int64]int{},
		pageMax:      map[int64]int{},
		pageStarted:  make(chan struct{}, 8),
		releasePages: make(chan struct{}),
		imageStarts:  map[int64][]time.Time{},
	}
	fetch := sourceengine.NewFetcher(engine, t.TempDir(), policies)
	dispatcher := download.New(client, fetch, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, defaults, nil).
		WithSourceThroughputPolicies(policies)

	done := make(chan error, 1)
	go func() {
		_, err := dispatcher.RunOnce(ctx)
		done <- err
	}()
	waitForPageStarts(t, engine.pageStarted, 3)
	engine.mu.Lock()
	assertAdmissionMaxima(t, engine.pageMax)
	engine.mu.Unlock()
	close(engine.releasePages)
	waitForIntegratedCycle(t, done)

	if got := countStates(ctx, t, client, limitedIDs)["downloaded"]; got != 2 {
		t.Errorf("limited source downloaded = %d, want 2", got)
	}
	if got := countStates(ctx, t, client, inheritedIDs)["downloaded"]; got != 2 {
		t.Errorf("inherited source downloaded = %d, want 2", got)
	}
	engine.mu.Lock()
	limitedStarts := append([]time.Time(nil), engine.imageStarts[101]...)
	inheritedStarts := append([]time.Time(nil), engine.imageStarts[202]...)
	engine.mu.Unlock()
	assertPacedImageStarts(t, limitedStarts)
	assertInheritedImageStarts(t, inheritedStarts, limitedStarts[1])
}

func waitForPageStarts(t *testing.T, started <-chan struct{}, count int) {
	t.Helper()
	for range count {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for asymmetric dispatcher admission")
		}
	}
}

func assertAdmissionMaxima(t *testing.T, maxima map[int64]int) {
	t.Helper()
	if maxima[101] != 1 || maxima[202] != 2 {
		t.Errorf("page admission maxima = %v, want source 101=1 and source 202=2", maxima)
	}
}

func waitForIntegratedCycle(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for integrated download cycle")
	}
}

func assertPacedImageStarts(t *testing.T, starts []time.Time) {
	t.Helper()
	if len(starts) != 3 {
		t.Fatalf("source 101 image attempts = %d, want 3 including one retry", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if gap := starts[i].Sub(starts[i-1]); gap < 700*time.Millisecond {
			t.Errorf("source 101 image start gap %d = %v, want approximately 750ms pacing", i, gap)
		}
	}
}

func assertInheritedImageStarts(t *testing.T, starts []time.Time, pacedRetry time.Time) {
	t.Helper()
	if len(starts) != 2 {
		t.Fatalf("source 202 image attempts = %d, want 2", len(starts))
	}
	for i, started := range starts {
		if !started.Before(pacedRetry) {
			t.Errorf("inherited source image start %d = %v, want before source 101's first paced retry at %v", i, started, pacedRetry)
		}
	}
}

func integrationJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode integration JPEG: %v", err)
	}
	return buf.Bytes()
}
