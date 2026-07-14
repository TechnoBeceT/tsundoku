// Package providers is the composition root for the real tracker set: the
// ONE place that depends on both internal/tracker (the Tracker contract)
// and every concrete tracker package (anilist, mal), assembling them into a
// ready tracker.Registry.
//
// It lives in its own package for exactly the reason
// internal/metadata/providers does (see that package's doc comment): every
// concrete tracker package imports internal/tracker for the Tracker
// contract it implements, so internal/tracker importing them back would be
// a real import cycle. This package sits ABOVE the cycle.
package providers

import (
	"net/http"

	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/anilist"
	"github.com/technobecet/tsundoku/internal/tracker/kitsu"
	"github.com/technobecet/tsundoku/internal/tracker/mal"
	"github.com/technobecet/tsundoku/internal/tracker/mangaupdates"
)

// Config configures the real tracker set NewRegistry builds. HTTPClient,
// when nil, lets each tracker construct its own rate-limited default client
// (see each tracker's own New doc comment). A blank client-id disables that
// tracker's OAuth connect (AuthURL fails closed with
// tracker.ErrClientIDNotConfigured) while leaving the rest of this package
// usable — the fleet "blank disables" pattern (spec §2), NOT a construction
// error: NewRegistry always builds all four trackers so GET /api/trackers
// (3c) can still list a disabled OAuth tracker with isLoggedIn=false. Kitsu
// and MangaUpdates need no client-id at all (credential login — see their
// own New doc comments), so Config carries no field for either.
type Config struct {
	AniListClientID string
	MALClientID     string
	// MALClientSecret is MAL's registered app client secret
	// (config.TrackerConfig.MALClientSecret) — blank leaves mal.Client
	// sending none (a public/"other"-type app); non-blank is required for a
	// CONFIDENTIAL MAL app's token exchange to succeed (see mal.New's doc
	// comment).
	MALClientSecret string
	HTTPClient      *http.Client
}

// NewRegistry builds the four Phase-3 real trackers (AniList, MAL, Kitsu,
// MangaUpdates) and returns a ready tracker.Registry over them.
func NewRegistry(cfg Config) *tracker.Registry {
	return tracker.NewRegistry(
		anilist.New(cfg.AniListClientID, cfg.HTTPClient),
		mal.New(cfg.MALClientID, cfg.MALClientSecret, cfg.HTTPClient),
		kitsu.New(cfg.HTTPClient),
		mangaupdates.New(cfg.HTTPClient),
	)
}
