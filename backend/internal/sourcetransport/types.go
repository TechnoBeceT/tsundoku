package sourcetransport

import (
	"context"
	"errors"
	"time"

	"github.com/technobecet/tsundoku/internal/runtimepolicy"
)

var (
	// ErrSourceNotFound reports that the requested source is absent from the
	// current live engine catalog.
	ErrSourceNotFound = errors.New("source not found")
	// ErrCatalogUnavailable reports that the live installed-source catalog
	// could not be loaded. Updates fail closed before policy or intent writes.
	ErrCatalogUnavailable = errors.New("source catalog unavailable")
)

// ImageConnectionMode controls whether image requests use fresh or reused
// connections.
type ImageConnectionMode string

const (
	ImageConnectionFresh ImageConnectionMode = "fresh"
	ImageConnectionReuse ImageConnectionMode = "reuse"
)

// BypassSessionMode describes the session choice resolved by the network owner.
type BypassSessionMode string

const (
	BypassSessionDisabled   BypassSessionMode = "disabled"
	BypassSessionDisposable BypassSessionMode = "disposable"
	BypassSessionReusable   BypassSessionMode = "reusable"
)

// Override holds independently optional persisted transport settings.
type Override struct {
	ReuseBypassSession  *bool
	ImageConnectionMode *ImageConnectionMode
	KCEFPolicy          *runtimepolicy.KCEFPolicy
}

// Effective is the fully resolved policy for one source.
type Effective struct {
	ReuseBypassSession  bool
	BypassSessionMode   BypassSessionMode
	ImageConnectionMode ImageConnectionMode
	KCEFPolicy          runtimepolicy.KCEFPolicy
}

// Intent is the persisted desired-versus-applied runtime state for one source.
type Intent struct {
	SourceID         int64
	DesiredRevision  int64
	AppliedRevision  int64
	LastApplyAttempt *time.Time
	LastApplyError   string
}

// UpdateResult is the policy and runtime intent after one successful update.
type UpdateResult struct {
	Override  Override
	Effective Effective
	Intent    Intent
}

// Defaults resolves global and source-dependent transport defaults at use time.
type Defaults interface {
	ImageConnectionMode(context.Context) ImageConnectionMode
	ResolveBypassSession(context.Context, int64, *bool) (bool, BypassSessionMode, error)
}

// SourceCatalog validates the engine-host source identity before any policy or
// intent mutation.
type SourceCatalog interface {
	RequireSource(context.Context, int64) error
}

// PatchOperation selects how one persisted override changes.
type PatchOperation uint8

const (
	PatchKeep PatchOperation = iota
	PatchSet
	PatchClear
)

// PatchField represents keep, set, and clear without conflating omitted values
// with an explicit inherited setting.
type PatchField[T any] struct {
	Operation PatchOperation
	Value     T
}

// Set returns a patch field that persists value as an override.
func Set[T any](value T) PatchField[T] { return PatchField[T]{Operation: PatchSet, Value: value} }

// Clear returns a patch field that removes an override and restores inheritance.
func Clear[T any]() PatchField[T] { return PatchField[T]{Operation: PatchClear} }

// Patch changes both optional transport overrides independently.
type Patch struct {
	ReuseBypassSession  PatchField[bool]
	ImageConnectionMode PatchField[ImageConnectionMode]
	KCEFPolicy          PatchField[runtimepolicy.KCEFPolicy]
}
