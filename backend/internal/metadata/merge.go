package metadata

import "strings"

// MergeInput is Merge's input: every provider's raw SeriesMetadata, keyed
// by Provider.Key(), plus the priority Order to gap-fill scalars and
// sequence collection accumulation by (index 0 = PRIMARY).
type MergeInput struct {
	// Metas holds each provider's metadata, keyed by Provider.Key().
	Metas map[string]SeriesMetadata
	// Order lists provider keys in priority order; index 0 is primary. Keys
	// present in Metas but absent from Order are NOT merged (Order drives
	// the walk).
	Order []string
}

// Merge combines several providers' SeriesMetadata into one record per
// QCAT-228: collection fields (Genres/Tags/AltTitles/Authors/Links) are
// UNIONED across every provider in in.Metas (deduped, first-seen wins),
// while scalar fields (Title/Description/Status/Year/Score/Publisher) are
// PRIMARY-ANCHORED GAP-FILLED — walk in.Order and take the first
// non-empty/non-zero value, so a set primary value is never overridden by
// a lower-priority provider. CoverURL is deliberately left zero; the cover
// is chosen independently elsewhere. Output ordering is deterministic:
// collections accumulate in Order sequence, preserving first-seen order.
func Merge(in MergeInput) SeriesMetadata {
	var out SeriesMetadata

	genres := newCollector(strings.ToLower)
	tags := newCollector(strings.ToLower)
	altTitles := newCollector(func(at AltTitle) string {
		return strings.ToLower(strings.TrimSpace(at.Name))
	})
	authors := newCollector(func(a Author) string { return a.Name + "\x00" + a.Role })
	// Keyed by Label+URL (not Label alone): two providers can legitimately share
	// a label (e.g. both list an "AniList" link) while pointing at genuinely
	// different URLs — keying on Label only silently dropped the second one.
	// Mirrors the Authors dedup shape (composite key, first-seen wins).
	links := newCollector(func(l Link) string { return l.Label + "\x00" + l.URL })

	for _, key := range in.Order {
		meta, ok := in.Metas[key]
		if !ok {
			continue
		}

		gapFillScalars(&out, meta)

		for _, g := range meta.Genres {
			genres.add(g)
		}
		for _, t := range meta.Tags {
			tags.add(t)
		}
		for _, at := range meta.AltTitles {
			altTitles.add(at)
		}
		for _, a := range meta.Authors {
			authors.add(a)
		}
		for _, l := range meta.Links {
			links.add(l)
		}
	}

	out.Genres = genres.items
	out.Tags = tags.items
	out.AltTitles = altTitles.items
	out.Authors = authors.items
	out.Links = links.items
	// CoverURL intentionally left zero — chosen independently elsewhere.

	return out
}

// gapFillScalars sets each scalar field on out from meta only when out
// doesn't already carry a non-empty/non-zero value — the "primary first,
// take the first non-empty" gap-fill rule, applied one provider at a time
// as Merge walks Order.
func gapFillScalars(out *SeriesMetadata, meta SeriesMetadata) {
	if out.Title == "" {
		out.Title = meta.Title
	}
	if out.Description == "" {
		out.Description = meta.Description
	}
	if out.Status == "" {
		out.Status = meta.Status
	}
	if out.Year == 0 {
		out.Year = meta.Year
	}
	if out.Score == 0 {
		out.Score = meta.Score
	}
	if out.Publisher == "" {
		out.Publisher = meta.Publisher
	}
}

// collector accumulates values of type T while deduping by a derived key,
// preserving first-seen insertion order — the shared shape behind every
// collection union in Merge.
type collector[T any] struct {
	keyOf func(T) string
	seen  map[string]struct{}
	items []T
}

// newCollector builds an empty collector that dedupes items by keyOf.
func newCollector[T any](keyOf func(T) string) *collector[T] {
	return &collector[T]{keyOf: keyOf, seen: make(map[string]struct{})}
}

// add appends v unless its derived key was already seen.
func (c *collector[T]) add(v T) {
	key := c.keyOf(v)
	if _, dup := c.seen[key]; dup {
		return
	}
	c.seen[key] = struct{}{}
	c.items = append(c.items, v)
}
