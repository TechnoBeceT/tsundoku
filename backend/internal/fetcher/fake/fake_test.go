package fake_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
)

// ref builds a FetchRef with a fixed SeriesProviderID for table tests.
func ref(provider, key string) fetcher.FetchRef {
	return fetcher.FetchRef{
		Provider:         provider,
		Scanlator:        "group",
		Language:         "en",
		URL:              "https://example.com/" + key,
		SuwayomiID:       42,
		SeriesProviderID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
	}
}

// TestFake_DefaultPages verifies that the fake returns the default page count
// and that the pages are non-empty.
func TestFake_DefaultPages(t *testing.T) {
	t.Parallel()

	f := fake.New()
	r := ref("mangadex", "ch-1")
	pages, err := f.Fetch(context.Background(), r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pages.PageCount != len(pages.Pages) {
		t.Errorf("PageCount %d != len(Pages) %d", pages.PageCount, len(pages.Pages))
	}
	if len(pages.Pages) == 0 {
		t.Fatal("expected at least one page")
	}
}

// TestFake_WithPages verifies that WithPages(n) configures the page count.
func TestFake_WithPages(t *testing.T) {
	t.Parallel()

	for _, n := range []int{1, 3, 10} {
		n := n
		t.Run("pages="+itoa(n), func(t *testing.T) {
			t.Parallel()
			f := fake.New(fake.WithPages(n))
			r := ref("mangadex", "ch-1")
			pages, err := f.Fetch(context.Background(), r)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pages.PageCount != n {
				t.Errorf("PageCount = %d; want %d", pages.PageCount, n)
			}
			if len(pages.Pages) != n {
				t.Errorf("len(Pages) = %d; want %d", len(pages.Pages), n)
			}
		})
	}
}

// TestFake_Deterministic verifies that the same FetchRef produces identical
// page bytes across two separate Fetch calls.
func TestFake_Deterministic(t *testing.T) {
	t.Parallel()

	f := fake.New(fake.WithPages(3))
	r := ref("mangadex", "ch-5")

	first, err := f.Fetch(context.Background(), r)
	if err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	second, err := f.Fetch(context.Background(), r)
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}

	if len(first.Pages) != len(second.Pages) {
		t.Fatalf("page counts differ: %d vs %d", len(first.Pages), len(second.Pages))
	}
	for i := range first.Pages {
		if !bytes.Equal(first.Pages[i].Data, second.Pages[i].Data) {
			t.Errorf("page %d bytes differ between calls", i)
		}
		if first.Pages[i].Ext != second.Pages[i].Ext {
			t.Errorf("page %d ext differs: %q vs %q", i, first.Pages[i].Ext, second.Pages[i].Ext)
		}
	}
}

// TestFake_DifferentRefsDifferentBytes verifies that different FetchRefs
// produce different page bytes, confirming the determinism is ref-keyed.
func TestFake_DifferentRefsDifferentBytes(t *testing.T) {
	t.Parallel()

	f := fake.New(fake.WithPages(1))
	r1 := ref("mangadex", "ch-1")
	r2 := ref("mangadex", "ch-2")

	p1, err := f.Fetch(context.Background(), r1)
	if err != nil {
		t.Fatalf("ref1: unexpected error: %v", err)
	}
	p2, err := f.Fetch(context.Background(), r2)
	if err != nil {
		t.Fatalf("ref2: unexpected error: %v", err)
	}

	if bytes.Equal(p1.Pages[0].Data, p2.Pages[0].Data) {
		t.Error("different refs produced identical page bytes; determinism is not ref-keyed")
	}
}

// TestFake_FailFirst verifies that WithFailFirst causes the first call to
// error and all subsequent calls to succeed with the configured pages.
func TestFake_FailFirst(t *testing.T) {
	t.Parallel()

	f := fake.New(fake.WithPages(2), fake.WithFailFirst())
	r := ref("mangadex", "ch-1")

	// First call must error.
	_, err := f.Fetch(context.Background(), r)
	if err == nil {
		t.Fatal("first call: expected an error, got nil")
	}

	// Second call must succeed.
	pages, err := f.Fetch(context.Background(), r)
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if pages.PageCount != 2 {
		t.Errorf("second call: PageCount = %d; want 2", pages.PageCount)
	}

	// Third call must also succeed (fail-once, not fail-always).
	pages, err = f.Fetch(context.Background(), r)
	if err != nil {
		t.Fatalf("third call: unexpected error: %v", err)
	}
	if pages.PageCount != 2 {
		t.Errorf("third call: PageCount = %d; want 2", pages.PageCount)
	}
}

// TestFake_WithError verifies that WithError causes every call to return the
// configured error and zero pages.
func TestFake_WithError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("provider down")
	f := fake.New(fake.WithError(sentinel))
	r := ref("mangadex", "ch-1")

	for i := range 3 {
		_, err := f.Fetch(context.Background(), r)
		if !errors.Is(err, sentinel) {
			t.Errorf("call %d: want sentinel error; got %v", i+1, err)
		}
	}
}

// TestFake_ConcurrentSafe verifies that concurrent Fetch calls do not race on
// internal state. Run with: go test -race ./internal/fetcher/fake/...
func TestFake_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	f := fake.New(fake.WithPages(2), fake.WithFailFirst())
	r := ref("mangadex", "ch-1")

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			// Discard result; we only care that the race detector finds no issues.
			_, _ = f.Fetch(context.Background(), r)
		}()
	}
	wg.Wait()
}

// itoa is a minimal int-to-string helper used for sub-test names.
func itoa(n int) string {
	buf := make([]byte, 0, 4)
	if n == 0 {
		return "0"
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
