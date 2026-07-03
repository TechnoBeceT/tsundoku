package suwayomi

import "context"

// gqlFetchMangaData is the typed shape of the `data` field for the fetchManga
// mutation.
type gqlFetchMangaData struct {
	FetchManga struct {
		Manga gqlMangaNode `json:"manga"`
	} `json:"fetchManga"`
}

// fetchMangaMutation FORCES Suwayomi to contact the upstream source for a
// manga's full details (author/artist/genre/description), then returns the
// refreshed record. This is distinct from the manga(id) query behind
// MangaMeta, which only ever reads Suwayomi's cache — Search/Browse populate
// that cache with a LIGHTWEIGHT manga (title/cover/url only), so a caller that
// wants the rich fields must go through this mutation at least once per manga.
//
// Shape confirmed against Suwayomi v2.2.2100 by TestShape8_FetchMangaDetails
// (see e2e_test.go): input field is `id: Int!`; result field is `manga`.
const fetchMangaMutation = `
mutation FetchMangaDetails($id: Int!) {
  fetchManga(input: { id: $id }) {
    manga {` + mangaFieldSelection + `
    }
  }
}`

// FetchMangaDetails triggers the Suwayomi fetchManga mutation, which forces a
// live details fetch from the upstream source, and returns the enriched
// Manga (author/artist/genre/description populated when the source provides
// them). Call this on demand — e.g. once per manga a Discover card is
// hovered — never across a whole page of Search/Browse results, since each
// call is a real network round-trip to the source.
func (c *httpClient) FetchMangaDetails(ctx context.Context, mangaID int) (Manga, error) {
	vars := map[string]any{"id": mangaID}
	var data gqlFetchMangaData
	if err := c.doGraphQL(ctx, fetchMangaMutation, vars, &data); err != nil {
		return Manga{}, err
	}
	return data.FetchManga.Manga.toManga(), nil
}
