package anilist

// GraphQL query/mutation strings for AniList's tracker-SYNC surface
// (graphql.anilist.co) — DISTINCT from internal/metadata/anilist's queries,
// which cover the public METADATA-read half (search/media detail/cover) and
// need no auth. These carry the OWNER'S OWN reading-progress entry
// (MediaList), which requires the account's Bearer token.
//
// Shapes follow spec/trackers-and-rich-library-umbrella-v2 §7 verbatim
// (mined from the Suwayomi headless-server report, not re-derived) —
// build-tagged TestShapeTracker_AniList_* (shape_test.go) re-proves these
// live once the owner has an account token to run them with.

// searchQuery mirrors internal/metadata/anilist's search shape but adds
// siteUrl (TrackSearchResult.URL — "View on AniList") plus the
// Search-Enrichment fields (format/startDate/averageScore/description,
// confirmed against Suwayomi-Server's AnilistApi.kt/Komikku's own
// AnilistApi.kt search query): format is AniList's MediaFormat enum
// ("MANGA"/"NOVEL"/"ONE_SHOT"/…), startDate only selects year (a search hit
// never carries month/day precision on the reference clients either),
// averageScore is AniList's RAW 0-100 community average (kept unscaled —
// see tracker.TrackSearchResult.Score's own doc comment), description is
// requested as plain text (asHtml:false) so this port never has to strip
// AniList's default HTML wrapping.
const searchQuery = `
query ($search: String, $perPage: Int) {
  Page(perPage: $perPage) {
    media(search: $search, type: MANGA) {
      id
      title { romaji english native }
      coverImage { large }
      status
      chapters
      siteUrl
      format
      startDate { year }
      averageScore
      description(asHtml: false)
    }
  }
}`

// viewerQuery captures the logged-in account's id (needed to resolve "my"
// MediaList entry — AniList's MediaList query takes userId+mediaId, there
// is no "give me MY entry for this media" shortcut), display name, and
// score format (spec §4 — captured once at login into
// TrackerConnection.score_format).
const viewerQuery = `
query {
  Viewer {
    id
    name
    mediaListOptions { scoreFormat }
  }
}`

// mediaListEntrySelection is the field set shared by the get/save/update
// entry operations, so the three response shapes stay identical (all three
// need score/progress/status/dates back to build a TrackEntry).
const mediaListEntrySelection = `
    id
    mediaId
    status
    score(format: POINT_100)
    progress
    private
    startedAt { year month day }
    completedAt { year month day }`

// getEntryQuery reads the caller's own MediaList entry for one media, or
// null when the manga is not yet on the account's list at all.
const getEntryQuery = `
query ($userId: Int, $mediaId: Int) {
  MediaList(userId: $userId, mediaId: $mediaId) {` + mediaListEntrySelection + `
  }
}`

// saveEntryMutation creates a NEW MediaList entry (a bind) — AniList's
// SaveMediaListEntry upserts by mediaId when no id is given.
const saveEntryMutation = `
mutation ($mediaId: Int, $progress: Int, $status: MediaListStatus, $private: Boolean) {
  SaveMediaListEntry(mediaId: $mediaId, progress: $progress, status: $status, private: $private) {` + mediaListEntrySelection + `
  }
}`

// updateEntryMutation writes to an EXISTING MediaList entry by its own id
// (TrackEntry.LibraryID) — the same SaveMediaListEntry mutation, keyed by id
// instead of mediaId, per spec §7.
const updateEntryMutation = `
mutation ($id: Int, $progress: Int, $status: MediaListStatus, $scoreRaw: Int, $startedAt: FuzzyDateInput, $completedAt: FuzzyDateInput, $private: Boolean) {
  SaveMediaListEntry(id: $id, progress: $progress, status: $status, scoreRaw: $scoreRaw, startedAt: $startedAt, completedAt: $completedAt, private: $private) {` + mediaListEntrySelection + `
  }
}`

// deleteEntryMutation removes a MediaList entry by its own id.
const deleteEntryMutation = `
mutation ($id: Int) {
  DeleteMediaListEntry(id: $id) {
    deleted
  }
}`
