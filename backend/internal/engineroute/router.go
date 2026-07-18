package engineroute

import (
	"context"
	"sync"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// Router is a sourceengine.Client that dispatches each RPC to the engine-host
// instance a source is bound to, falling back to the DEFAULT instance for any
// unbound source. It is the ONE client seam every consumer holds — the
// dispatcher, ingest, imports, refresh, warm-up, cover proxy, and the HTTP
// handlers all target a *Router — so per-source routing is transparent to them.
//
// Only the SOURCE-ADDRESSED content calls (those taking a sourceID) are routed;
// engine-GLOBAL management calls (Health, Sources, Extensions, repos, and the
// FlareSolverr/SOCKS config push) always go to the default instance. The default
// instance is the authoritative registry the boot reconcile provisions;
// non-default instances are provisioned by ReconcileNetwork (which runs a full
// reconcile against each), so a routed source's own instance carries the same
// installed extensions.
//
// Concurrency: the routing table (source id → instance client) is swapped
// atomically under a mutex by SetRoutes, and read under the same mutex by every
// RPC, so a live reconcile can rebuild the table while requests are in flight.
//
// The zero value is not usable — construct one with NewRouter. With no routes
// installed (the deploy-day state, and the state whenever no source has a
// non-default binding) EVERY call delegates to the default instance, so the
// Router is byte-for-byte the single-instance client it wraps.
type Router struct {
	// defaultClient is the client for the default engine-host instance. It never
	// changes for the life of the Router.
	defaultClient sourceengine.Client

	mu     sync.RWMutex
	routes map[int64]sourceengine.Client // source id → its instance client
}

// Compile-time assertion: *Router must satisfy sourceengine.Client so it is a
// drop-in for the raw client everywhere.
var _ sourceengine.Client = (*Router)(nil)

// NewRouter constructs a Router that delegates everything to defaultClient until
// SetRoutes installs per-source overrides.
func NewRouter(defaultClient sourceengine.Client) *Router {
	return &Router{
		defaultClient: defaultClient,
		routes:        map[int64]sourceengine.Client{},
	}
}

// SetRoutes atomically REPLACES the source-id → instance routing table. A source
// absent from routes reverts to the default instance. Passing an empty (or nil)
// map clears all overrides — the byte-for-byte-default state. ReconcileNetwork
// calls this after (re)deriving profiles and ensuring their instances.
func (r *Router) SetRoutes(routes map[int64]sourceengine.Client) {
	next := make(map[int64]sourceengine.Client, len(routes))
	for id, c := range routes {
		if c != nil {
			next[id] = c
		}
	}
	r.mu.Lock()
	r.routes = next
	r.mu.Unlock()
}

// clientFor returns the instance client for sourceID — its bound override if one
// is installed, else the default instance.
func (r *Router) clientFor(sourceID int64) sourceengine.Client {
	r.mu.RLock()
	c, ok := r.routes[sourceID]
	r.mu.RUnlock()
	if ok {
		return c
	}
	return r.defaultClient
}

// --- source-addressed content calls: routed by sourceID ------------------

// Search routes to sourceID's instance.
func (r *Router) Search(ctx context.Context, sourceID int64, query string, page int) (sourceengine.SearchResult, error) {
	return r.clientFor(sourceID).Search(ctx, sourceID, query, page)
}

// Popular routes to sourceID's instance.
func (r *Router) Popular(ctx context.Context, sourceID int64, page int) (sourceengine.SearchResult, error) {
	return r.clientFor(sourceID).Popular(ctx, sourceID, page)
}

// Latest routes to sourceID's instance.
func (r *Router) Latest(ctx context.Context, sourceID int64, page int) (sourceengine.SearchResult, error) {
	return r.clientFor(sourceID).Latest(ctx, sourceID, page)
}

// MangaDetails routes to sourceID's instance.
func (r *Router) MangaDetails(ctx context.Context, sourceID int64, url string) (sourceengine.MangaDetails, error) {
	return r.clientFor(sourceID).MangaDetails(ctx, sourceID, url)
}

// Chapters routes to sourceID's instance.
func (r *Router) Chapters(ctx context.Context, sourceID int64, url, mangaTitle string) ([]sourceengine.Chapter, error) {
	return r.clientFor(sourceID).Chapters(ctx, sourceID, url, mangaTitle)
}

// Pages routes to sourceID's instance.
func (r *Router) Pages(ctx context.Context, sourceID int64, chapterURL string) ([]sourceengine.Page, error) {
	return r.clientFor(sourceID).Pages(ctx, sourceID, chapterURL)
}

// Image routes to sourceID's instance — the load-bearing egress: a bound
// source's page bytes are fetched by its own instance (over its VPN/proxy),
// which is the whole point of the feature.
func (r *Router) Image(ctx context.Context, sourceID int64, pageURL, imageURL string) ([]byte, string, error) {
	return r.clientFor(sourceID).Image(ctx, sourceID, pageURL, imageURL)
}

// Preferences routes to sourceID's instance.
func (r *Router) Preferences(ctx context.Context, sourceID int64) ([]sourceengine.Preference, error) {
	return r.clientFor(sourceID).Preferences(ctx, sourceID)
}

// SetPreferences routes to sourceID's instance.
func (r *Router) SetPreferences(ctx context.Context, sourceID int64, changes map[string]any) ([]sourceengine.Preference, error) {
	return r.clientFor(sourceID).SetPreferences(ctx, sourceID, changes)
}

// --- engine-global management calls: always the default instance ---------

// Health probes the default instance.
func (r *Router) Health(ctx context.Context) (sourceengine.Health, error) {
	return r.defaultClient.Health(ctx)
}

// Sources lists the default instance's loaded sources (the authoritative
// registry; non-default instances mirror its installed extensions).
func (r *Router) Sources(ctx context.Context) ([]sourceengine.Source, error) {
	return r.defaultClient.Sources(ctx)
}

// Extensions lists the default instance's extensions.
func (r *Router) Extensions(ctx context.Context) ([]sourceengine.Extension, error) {
	return r.defaultClient.Extensions(ctx)
}

// InstallExtension installs on the default instance. ReconcileNetwork mirrors the
// install onto non-default instances by running a full reconcile against each.
func (r *Router) InstallExtension(ctx context.Context, pkgName, apkURL string) ([]sourceengine.Extension, error) {
	return r.defaultClient.InstallExtension(ctx, pkgName, apkURL)
}

// RefreshExtensions refreshes the default instance's available-extensions list.
func (r *Router) RefreshExtensions(ctx context.Context) ([]sourceengine.Extension, error) {
	return r.defaultClient.RefreshExtensions(ctx)
}

// UpdateExtension updates on the default instance.
func (r *Router) UpdateExtension(ctx context.Context, pkgName string) ([]sourceengine.Extension, error) {
	return r.defaultClient.UpdateExtension(ctx, pkgName)
}

// UninstallExtension uninstalls on the default instance.
func (r *Router) UninstallExtension(ctx context.Context, pkgName string) ([]sourceengine.Extension, error) {
	return r.defaultClient.UninstallExtension(ctx, pkgName)
}

// Repos reads the default instance's configured repos.
func (r *Router) Repos(ctx context.Context) ([]string, error) {
	return r.defaultClient.Repos(ctx)
}

// SetRepos writes the default instance's repos.
func (r *Router) SetRepos(ctx context.Context, repos []string) ([]string, error) {
	return r.defaultClient.SetRepos(ctx, repos)
}

// SetFlareSolverr pushes the GLOBAL FlareSolverr config onto the default
// instance. Per-profile FlareSolverr is pushed by ReconcileNetwork onto each
// non-default instance directly (never through the Router).
func (r *Router) SetFlareSolverr(ctx context.Context, patch sourceengine.FlareSolverrPatch) (sourceengine.FlareSolverrConfig, error) {
	return r.defaultClient.SetFlareSolverr(ctx, patch)
}

// SetSocks pushes the GLOBAL SOCKS config onto the default instance. Per-profile
// SOCKS is pushed by ReconcileNetwork onto each non-default instance directly.
func (r *Router) SetSocks(ctx context.Context, patch sourceengine.SocksPatch) (sourceengine.SocksConfig, error) {
	return r.defaultClient.SetSocks(ctx, patch)
}
