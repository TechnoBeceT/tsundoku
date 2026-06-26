// Package category is the library-category domain: the user-managed set of
// top-level library folders that group series (Manga, Manhwa, …, or any
// owner-defined bucket). It owns category CRUD (create / list-with-counts /
// rename / reorder / delete-when-empty), the startup seed+backfill that
// guarantees the five defaults exist and every series is linked, and the
// find-or-create helper that lets a category folder on disk round-trip into a
// Category row during reconcile.
//
// The Ent predicate package internal/ent/category collides with this package
// name and must be imported aliased (entcategory) wherever both are needed.
package category

import (
	"errors"

	"github.com/technobecet/tsundoku/internal/ent"
)

// ErrCategoryNotFound is returned when no category matches the given id. The
// HTTP handler maps it to a 404.
var ErrCategoryNotFound = errors.New("category not found")

// ErrCategoryNameTaken is returned by Create and Rename when another category
// already uses the requested name. The HTTP handler maps it to a 409.
var ErrCategoryNameTaken = errors.New("category name already in use")

// ErrCategoryNotEmpty is returned by Delete when the category still has series
// filed under it. The HTTP handler maps it to a 409.
var ErrCategoryNotEmpty = errors.New("category is not empty")

// ErrCategoryProtected is returned by Rename and Delete for the protected
// default ("Other"), which must always remain as a safe fallback. The HTTP
// handler maps it to a 400.
var ErrCategoryProtected = errors.New("category is protected")

// ErrInvalidCategoryName is returned by ValidateName (and the create/rename
// paths) when a name is blank, too long, or not filesystem-safe. The HTTP
// handler maps it to a 400.
var ErrInvalidCategoryName = errors.New("invalid category name")

// Service is the category domain service. It owns the Ent client and the
// storage root so a rename can move the category folder on disk in lockstep
// with the DB (the M3 disk-folder-determining invariant, now for categories).
type Service struct {
	client  *ent.Client
	storage string
}

// NewService builds the category service bound to an Ent client and the library
// storage root (used by Rename to move the on-disk category folder).
func NewService(client *ent.Client, storage string) *Service {
	return &Service{client: client, storage: storage}
}
