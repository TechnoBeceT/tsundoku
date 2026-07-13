// Package providers is the composition root for the real metadata provider
// set: it is the ONE place that depends on both internal/metadata (the
// Provider contract) and every concrete provider package (anilist,
// mangadex, mangaupdates, mal, kitsu), assembling them into a ready
// metadata.Registry.
//
// It lives in its own package precisely BECAUSE internal/metadata cannot
// import the concrete providers itself: every provider package imports
// internal/metadata for the Provider contract it implements (each has a
// compile-time `var _ metadata.Provider = (*Client)(nil)` assert), so
// internal/metadata importing them back is a real import cycle (`go build`
// rejects it). This package sits ABOVE the cycle — nothing in
// internal/metadata imports providers, and providers→metadata /
// providers→anilist→metadata are both fine. The result: registry wiring is
// explicit and compile-enforced (no global factory, no runtime
// registration), mirroring how this codebase already wires pluggable
// implementations at a composition layer rather than inside the
// interface-owning package (e.g. main.go builds suwayomi.NewClient and
// hands it to download.New).
package providers

import (
	"net/http"

	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/metadata/anilist"
	"github.com/technobecet/tsundoku/internal/metadata/kitsu"
	"github.com/technobecet/tsundoku/internal/metadata/mal"
	"github.com/technobecet/tsundoku/internal/metadata/mangadex"
	"github.com/technobecet/tsundoku/internal/metadata/mangaupdates"
)

// Config configures the real metadata provider set NewRegistry builds.
// HTTPClient, when nil, lets each provider construct its own default client
// (most wrap a provider-specific rate-limited transport — see each
// provider's own New doc comment). MALClientID is MyAnimeList's required
// app credential (mal.New) — MAL requests fail without one.
type Config struct {
	MALClientID string
	HTTPClient  *http.Client
}

// NewRegistry builds the five real metadata providers in their documented
// default priority order and returns a ready metadata.Registry over them.
// Index 0 is the highest priority — the merge PRIMARY (see
// metadata.Provider.Priority()'s lower-number-wins convention): anilist(0),
// mangadex(1), mangaupdates(2), mal(3), kitsu(4).
//
// The order here IS the registry's priority order (metadata.NewRegistry
// preserves call order); it matches each provider's own Priority() constant,
// which is asserted by providers_test.go.
func NewRegistry(cfg Config) *metadata.Registry {
	ps := []metadata.Provider{
		anilist.New(cfg.HTTPClient),
		mangadex.New(cfg.HTTPClient),
		mangaupdates.New(cfg.HTTPClient),
		mal.New(cfg.MALClientID, cfg.HTTPClient),
		kitsu.New(cfg.HTTPClient),
	}
	return metadata.NewRegistry(ps...)
}
