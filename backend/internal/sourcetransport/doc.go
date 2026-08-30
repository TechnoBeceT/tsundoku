// Package sourcetransport persists optional per-source transport overrides and
// the revisioned runtime intent that tells engine convergence what remains to
// be applied. Failed revisions retry on startup and the existing download job
// cadence. ApplyRevisions converges an exact committed batch with one full
// runtime snapshot and bounded bulk intent writes; its locked post-apply
// recheck excludes revisions superseded or removed during convergence. When
// the applier exposes the shared convergence lifecycle, each complete apply
// call (including bounded failure metadata) remains admitted until it finishes,
// so shutdown cannot close its database or launcher underneath it. Source
// identity stays with the injected live engine catalog.
package sourcetransport
