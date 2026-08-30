// Package sourcetransport persists optional per-source transport overrides and
// the revisioned runtime intent that tells engine convergence what remains to
// be applied. Failed revisions retry on startup and the existing download job
// cadence. Source identity stays with the injected live engine catalog.
package sourcetransport
