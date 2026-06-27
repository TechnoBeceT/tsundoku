package category_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/ent"
)

// warnSentinel is a substring of the footgun-guard warning NameOf emits when the
// category edge is unloaded but category_id is set.
const warnSentinel = "category_id is set but the Category edge was not eager-loaded"

// captureDefaultLogger redirects the default slog logger to buf for the duration
// of a test and returns a restore func. NameOf logs through slog.Warn (the default
// logger), so this lets the test assert on the warn-path output.
func captureDefaultLogger(buf *bytes.Buffer) func() {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	return func() { slog.SetDefault(prev) }
}

// TestNameOf_LoadedEdgeReturnsNameNoWarn verifies the happy path: a loaded edge
// yields the category name and emits no footgun warning.
func TestNameOf_LoadedEdgeReturnsNameNoWarn(t *testing.T) {
	var buf bytes.Buffer
	defer captureDefaultLogger(&buf)()

	s := &ent.Series{ID: uuid.New(), CategoryID: uuid.New()}
	s.Edges.Category = &ent.Category{Name: "Manhwa"}

	if got := category.NameOf(s); got != "Manhwa" {
		t.Fatalf("NameOf with loaded edge: got %q, want %q", got, "Manhwa")
	}
	if strings.Contains(buf.String(), warnSentinel) {
		t.Fatalf("did not expect a footgun warning on the happy path; log=%q", buf.String())
	}
}

// TestNameOf_UnloadedEdgeButCategoryIDSetWarns is the footgun proof: the scalar
// category_id is set (so the series HAS a category) but the edge was not loaded.
// NameOf must return "" AND emit the loud warning so the missing WithCategory()
// is surfaced instead of silently mislocating the series on disk.
func TestNameOf_UnloadedEdgeButCategoryIDSetWarns(t *testing.T) {
	var buf bytes.Buffer
	defer captureDefaultLogger(&buf)()

	s := &ent.Series{ID: uuid.New(), CategoryID: uuid.New()} // edge deliberately nil

	if got := category.NameOf(s); got != "" {
		t.Fatalf("NameOf with unloaded edge: got %q, want \"\"", got)
	}
	if !strings.Contains(buf.String(), warnSentinel) {
		t.Fatalf("expected the footgun warning; log=%q", buf.String())
	}
}

// TestNameOf_NoCategoryIDIsSilent verifies the legitimate category-less case
// (category_id == uuid.Nil, no edge): NameOf returns "" with NO warning, since
// this is the genuine pre-backfill state, not a forgotten eager-load.
func TestNameOf_NoCategoryIDIsSilent(t *testing.T) {
	var buf bytes.Buffer
	defer captureDefaultLogger(&buf)()

	s := &ent.Series{ID: uuid.New()} // CategoryID == uuid.Nil, edge nil

	if got := category.NameOf(s); got != "" {
		t.Fatalf("NameOf with no category: got %q, want \"\"", got)
	}
	if strings.Contains(buf.String(), warnSentinel) {
		t.Fatalf("did not expect a warning for a genuinely category-less series; log=%q", buf.String())
	}
}

// TestNameOf_NilSeriesIsSilent verifies a nil series returns "" without panicking
// or warning.
func TestNameOf_NilSeriesIsSilent(t *testing.T) {
	var buf bytes.Buffer
	defer captureDefaultLogger(&buf)()

	if got := category.NameOf(nil); got != "" {
		t.Fatalf("NameOf(nil): got %q, want \"\"", got)
	}
	if strings.Contains(buf.String(), warnSentinel) {
		t.Fatalf("did not expect a warning for a nil series; log=%q", buf.String())
	}
}
