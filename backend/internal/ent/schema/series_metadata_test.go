package schema_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/metadata"
)

// wantSeriesMetadata bundles the rich-metadata fixture values shared by the
// round-trip and zero-value tests below, so each test's assertions can be
// driven by one small loop instead of a long if-chain (keeps cyclomatic
// complexity low while still checking every field).
type wantSeriesMetadata struct {
	genres, tags   []string
	altTitles      []metadata.AltTitle
	authors        []metadata.Author
	links          []metadata.Link
	year           int
	metadataSource *metadata.SourceRef
	coverSource    *metadata.SourceRef
}

func fullWantSeriesMetadata() wantSeriesMetadata {
	return wantSeriesMetadata{
		genres: []string{"Action", "Fantasy"},
		tags:   []string{"Reincarnation", "Overpowered MC"},
		altTitles: []metadata.AltTitle{
			{Name: "俺だけレベルアップな件", Type: "NATIVE", Lang: "ja"},
			{Name: "Only I Level Up", Type: "SYNONYM", Lang: "en"},
		},
		authors: []metadata.Author{
			{Name: "Chugong", Role: "STORY"},
			{Name: "Dubu", Role: "ART"},
		},
		links: []metadata.Link{
			{Label: "AniList", URL: "https://anilist.co/manga/105398"},
		},
		year: 2016,
		metadataSource: &metadata.SourceRef{
			Kind: "metadata", Ref: "anilist", RemoteID: "105398",
			RemoteURL: "https://anilist.co/manga/105398",
		},
		coverSource: &metadata.SourceRef{
			Kind: "metadata", Ref: "mangadex", RemoteID: "abc-123",
			RemoteURL: "https://mangadex.org/title/abc-123",
		},
	}
}

// namedField pairs a field's label with a got/want pair so the caller's
// assertion loop stays flat regardless of how many rich fields exist.
type namedField struct {
	name     string
	got      any
	want     any
	nilCheck bool // true when got/want are pointers that must both be non-nil to compare
}

// assertRichMetadataFields walks a slice of (name, got, want) pairs and
// reports a mismatch for each — the ONE comparison loop both tests below
// reuse, so adding a field never grows a function's branch count.
func assertRichMetadataFields(t *testing.T, fields []namedField) {
	t.Helper()
	for _, f := range fields {
		if f.nilCheck {
			gv := reflect.ValueOf(f.got)
			wv := reflect.ValueOf(f.want)
			if gv.IsNil() != wv.IsNil() {
				t.Errorf("%s = %#v, want %#v", f.name, f.got, f.want)
				continue
			}
			if !gv.IsNil() && !reflect.DeepEqual(gv.Elem().Interface(), wv.Elem().Interface()) {
				t.Errorf("%s = %#v, want %#v", f.name, f.got, f.want)
			}
			continue
		}
		if !reflect.DeepEqual(f.got, f.want) {
			t.Errorf("%s = %#v, want %#v", f.name, f.got, f.want)
		}
	}
}

// TestSeriesRichMetadataRoundTrips is the schema-level durability proof for
// the Phase-1 metadata engine's additive Series columns (genres/tags/
// alt_titles/authors/links/year/metadata_source/cover_source): every new
// field must survive a save + reload byte-for-byte, since disk.Reconcile
// (the disk-package round-trip proof) writes through exactly these same
// generated setters.
func TestSeriesRichMetadataRoundTrips(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	want := fullWantSeriesMetadata()

	created := client.Series.Create().
		SetTitle("Solo Leveling").
		SetSlug("solo-leveling-metadata-roundtrip").
		SetGenres(want.genres).
		SetTags(want.tags).
		SetAltTitles(want.altTitles).
		SetAuthors(want.authors).
		SetLinks(want.links).
		SetYear(want.year).
		SetMetadataSource(want.metadataSource).
		SetCoverSource(want.coverSource).
		SaveX(ctx)

	// Reload from the DB — a fresh Get, not the in-memory create result — so
	// the assertions prove the jsonb columns actually persisted and decoded,
	// not just that the in-process struct still holds what we set.
	reloaded, err := client.Series.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Series.Get after create: %v", err)
	}

	assertRichMetadataFields(t, []namedField{
		{name: "Genres", got: reloaded.Genres, want: want.genres},
		{name: "Tags", got: reloaded.Tags, want: want.tags},
		{name: "AltTitles", got: reloaded.AltTitles, want: want.altTitles},
		{name: "Authors", got: reloaded.Authors, want: want.authors},
		{name: "Links", got: reloaded.Links, want: want.links},
		{name: "Year", got: reloaded.Year, want: want.year},
		{name: "MetadataSource", got: reloaded.MetadataSource, want: want.metadataSource, nilCheck: true},
		{name: "CoverSource", got: reloaded.CoverSource, want: want.coverSource, nilCheck: true},
	})
}

// TestSeriesRichMetadataDefaultsToZeroValue proves the migration-safety claim:
// a series created WITHOUT touching any rich-metadata field (the shape of
// every pre-existing row after Ent's additive auto-migrate) gets nil
// collections/descriptors and Year=0 — never an error, never a required
// field — so an upgrade never fails to migrate existing data.
func TestSeriesRichMetadataDefaultsToZeroValue(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	s := client.Series.Create().
		SetTitle("Untouched Series").
		SetSlug("untouched-series-metadata-defaults").
		SaveX(ctx)

	if s.Genres != nil {
		t.Errorf("Genres = %#v, want nil", s.Genres)
	}
	if s.Tags != nil {
		t.Errorf("Tags = %#v, want nil", s.Tags)
	}
	if s.AltTitles != nil {
		t.Errorf("AltTitles = %#v, want nil", s.AltTitles)
	}
	if s.Authors != nil {
		t.Errorf("Authors = %#v, want nil", s.Authors)
	}
	if s.Links != nil {
		t.Errorf("Links = %#v, want nil", s.Links)
	}
	if s.Year != 0 {
		t.Errorf("Year = %d, want 0", s.Year)
	}
	if s.MetadataSource != nil {
		t.Errorf("MetadataSource = %#v, want nil", s.MetadataSource)
	}
	if s.CoverSource != nil {
		t.Errorf("CoverSource = %#v, want nil", s.CoverSource)
	}
}
