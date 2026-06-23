// Package fake provides a deterministic in-memory implementation of
// fetcher.ChapterFetcher for use in tests.
//
// The fake is safe for concurrent use and is intentionally isolated in this
// sub-package so that production wiring can never accidentally import it.
// Configure it with functional options (WithPages, WithFailFirst, WithError)
// to drive every download-dispatcher and upgrade-engine test path without
// any network or Suwayomi dependency.
package fake

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/technobecet/tsundoku/internal/fetcher"
)

// defaultPageCount is the number of pages returned when WithPages is not set.
const defaultPageCount = 5

// pageExt is the file extension reported for every synthesised page.
const pageExt = "png"

// errFailFirst is the sentinel error returned on the first call when
// WithFailFirst is active.
var errFailFirst = errors.New("fake: transient fetch failure (fail-first mode)")

// Option is a functional option that configures a Fetcher.
type Option func(*Fetcher)

// WithPages sets the number of pages the fake returns on each successful Fetch
// call. Panics if n < 1.
func WithPages(n int) Option {
	if n < 1 {
		panic(fmt.Sprintf("fake.WithPages: n must be >= 1, got %d", n))
	}
	return func(f *Fetcher) {
		f.pageCount = n
	}
}

// WithFailFirst configures the fake to return an error on the very first Fetch
// call and succeed on all subsequent calls. This lets dispatcher tests drive the
// retry-then-succeed path without any network involvement.
func WithFailFirst() Option {
	return func(f *Fetcher) {
		f.failFirst = true
	}
}

// WithError configures the fake to return the given error on every Fetch call.
// Use this to drive the permanent-failure path in dispatcher and upgrade tests.
// The provided error is returned as-is so callers can use errors.Is for
// assertion.
func WithError(err error) Option {
	return func(f *Fetcher) {
		f.alwaysErr = err
	}
}

// Fetcher is the deterministic in-memory implementation of
// fetcher.ChapterFetcher. Create one with New and configure it with Options.
// Fetcher is safe for concurrent use.
type Fetcher struct {
	mu        sync.Mutex
	pageCount int
	failFirst bool
	alwaysErr error
	called    bool // tracks whether the first call has been made
}

// New returns a new Fetcher configured by the given options. Without options
// the fake returns defaultPageCount pages deterministically, never errors, and
// is safe for concurrent use.
func New(opts ...Option) *Fetcher {
	f := &Fetcher{
		pageCount: defaultPageCount,
	}
	for _, o := range opts {
		o(f)
	}
	return f
}

// Fetch returns the deterministic pages for the given ref, or an error when
// configured to fail. Page bytes are derived from a SHA-256 hash of the ref
// fields and the page index, so the same ref always produces the same bytes
// across calls and different refs produce different bytes.
//
// Error precedence:
//  1. WithError — always returns the configured error.
//  2. WithFailFirst — returns errFailFirst on the first call only.
//  3. Otherwise — returns a populated ChapterPages.
func (f *Fetcher) Fetch(_ context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Always-error mode takes priority.
	if f.alwaysErr != nil {
		return fetcher.ChapterPages{}, f.alwaysErr
	}

	// Fail-first mode: error on the very first call, succeed thereafter.
	if f.failFirst && !f.called {
		f.called = true
		return fetcher.ChapterPages{}, errFailFirst
	}
	f.called = true

	pages := make([]fetcher.PageImage, f.pageCount)
	for i := range f.pageCount {
		pages[i] = fetcher.PageImage{
			Data: synthesisePage(ref, i),
			Ext:  pageExt,
		}
	}
	return fetcher.ChapterPages{
		Pages:     pages,
		PageCount: f.pageCount,
	}, nil
}

// synthesisePage builds a deterministic byte slice for page i of the given ref.
//
// The bytes are the raw SHA-256 digest of:
//
//	provider + "\x00" + scanlator + "\x00" + language + "\x00" + url + "\x00"
//	+ big-endian int64(suwayomiID) + seriesProviderID bytes + big-endian int64(pageIndex)
//
// This scheme guarantees:
//   - Same ref + same index → identical bytes (determinism).
//   - Different URL/index → different hash (no collisions in practice).
func synthesisePage(ref fetcher.FetchRef, pageIndex int) []byte {
	h := sha256.New()

	// Null-terminated string fields for collision resistance.
	for _, s := range []string{ref.Provider, ref.Scanlator, ref.Language, ref.URL} {
		_, _ = fmt.Fprintf(h, "%s\x00", s)
	}

	// Fixed-width numeric/UUID fields. The int→int64 cast is intentional: we
	// want the raw bit-pattern for hashing, sign-extension is harmless here, and
	// SuwayomiID / pageIndex are always non-negative in practice.
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(int64(ref.SuwayomiID))) //nolint:gosec
	_, _ = h.Write(buf[:])
	_, _ = h.Write(ref.SeriesProviderID[:])
	binary.BigEndian.PutUint64(buf[:], uint64(int64(pageIndex))) //nolint:gosec
	_, _ = h.Write(buf[:])

	return h.Sum(nil)
}
