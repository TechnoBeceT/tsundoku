package metadata

// SourceRef identifies where a piece of merged metadata (or a chosen cover)
// came from, for owner-facing provenance display and later re-fetch.
type SourceRef struct {
	// Kind is "metadata" (v1) | "source" | "tracker" (later).
	Kind string `json:"kind"`
	// Ref is the provider Key() | SeriesProvider UUID | tracker id.
	Ref       string `json:"ref"`
	RemoteID  string `json:"remoteId"`
	RemoteURL string `json:"remoteUrl"`
}
