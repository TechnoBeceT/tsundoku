// Package api embeds the OpenAPI 3.1 contract so it can be served at runtime
// without requiring the YAML file to be present on the deployed filesystem.
//
// The spec file lives at backend/api/openapi.yaml (repository root) and is
// the single source of truth for the Tsundoku HTTP surface. Update it in the
// same commit as any endpoint change (ENGINEERING.md §14).
package api

import _ "embed"

// Spec is the raw OpenAPI 3.1 YAML embedded at build time.
// Consumers (e.g. internal/server/docs.go) serve it directly or parse it for
// validation.
//
//go:embed openapi.yaml
var Spec []byte
