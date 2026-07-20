// Package ent provides the generated Ent ORM client for Tsundoku.
//
// EVERYTHING in this package except the schema/ subdirectory is GENERATED — never
// hand-edit it. The entity definitions in schema/ are the only hand-written source;
// edit those, then regenerate with `go generate ./internal/ent`. A hand-edit outside
// schema/ is silently destroyed by the next regeneration, so it looks like a working
// change right up until someone touches an unrelated schema.
package ent

//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate ./schema
