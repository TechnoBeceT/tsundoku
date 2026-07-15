package sourceengine

import (
	"context"
	"net/http"
	"net/url"
)

// installRequest is the wire body for POST /extensions/install. Both fields
// use omitempty so that only the non-empty one of {pkgName, apkUrl} the
// caller supplied is sent — the engine host requires exactly one.
type installRequest struct {
	PkgName string `json:"pkgName,omitempty"`
	ApkURL  string `json:"apkUrl,omitempty"`
}

// extensionPath builds the /extensions/{pkgName} path, escaping pkgName so a
// package name can never be mistaken for extra path segments.
func extensionPath(pkgName string) string {
	return "/extensions/" + url.PathEscape(pkgName)
}

// Extensions calls GET /extensions to list every extension the engine host
// knows about (installed + available from the configured repos).
func (c *httpClient) Extensions(ctx context.Context) ([]Extension, error) {
	return get[[]Extension](ctx, c, "/extensions")
}

// InstallExtension calls POST /extensions/install, sending only the
// non-empty one of {pkgName, apkUrl}, and returns the refreshed extension
// list. Exactly one of pkgName/apkURL must be non-empty (enforced by the
// engine host, not this client).
func (c *httpClient) InstallExtension(ctx context.Context, pkgName, apkURL string) ([]Extension, error) {
	return post[[]Extension](ctx, c, "/extensions/install", installRequest{PkgName: pkgName, ApkURL: apkURL})
}

// RefreshExtensions calls POST /extensions/refresh to re-fetch the
// available-extensions list from the configured repos and returns the
// refreshed list.
func (c *httpClient) RefreshExtensions(ctx context.Context) ([]Extension, error) {
	return post[[]Extension](ctx, c, "/extensions/refresh", nil)
}

// UpdateExtension calls POST /extensions/{pkgName}/update to update an
// already-installed extension and returns the refreshed extension list.
func (c *httpClient) UpdateExtension(ctx context.Context, pkgName string) ([]Extension, error) {
	return post[[]Extension](ctx, c, extensionPath(pkgName)+"/update", nil)
}

// UninstallExtension calls DELETE /extensions/{pkgName} to remove an
// installed extension and returns the refreshed extension list.
func (c *httpClient) UninstallExtension(ctx context.Context, pkgName string) ([]Extension, error) {
	return doJSON[[]Extension](ctx, c, http.MethodDelete, extensionPath(pkgName), nil)
}
