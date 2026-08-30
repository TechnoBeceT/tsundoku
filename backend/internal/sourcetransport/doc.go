// Package sourcetransport persists optional per-source transport overrides and
// the revisioned runtime intent that tells engine convergence what remains to
// be applied. Failed revisions retry on startup and the existing download job
// cadence. When the applier exposes the shared convergence lifecycle, a full
// ApplyPending call (including bounded failure metadata) remains admitted until
// it finishes, so shutdown cannot close its database or launcher underneath it.
// Source identity stays with the injected live engine catalog.
package sourcetransport
