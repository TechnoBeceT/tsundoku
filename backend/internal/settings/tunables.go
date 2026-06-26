// Package settings is the runtime-tunable configuration overlay. It sits on top
// of the env-sourced config defaults (internal/config remains the SOLE env
// boundary) and exposes a CLOSED allowlist of keys whose values an owner may
// change at runtime — stored in the Settings DB table — without a restart.
//
// Resolution for every tunable is: the DB override if a (valid) row exists, else
// the config-resolved default injected at construction. Consumers (the download
// dispatcher, the job tickers, the refresh sweep, the health computation) read
// the typed accessors at the point of use, so a change takes effect on the next
// cycle (hot reload). The service NEVER reads the environment — main injects the
// Defaults built from *config.Config, preserving the single env boundary.
package settings

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrUnknownSetting is returned by Set when a key is not in the closed allowlist.
// The HTTP handler maps it to a 400 — the API never writes arbitrary keys.
var ErrUnknownSetting = errors.New("unknown setting key")

// ErrInvalidSetting is returned by Set when a value fails its per-key validation
// (unparseable or out of bounds). The HTTP handler maps it to a 400. Set is
// fail-closed: it rejects invalid values so the store never holds one.
var ErrInvalidSetting = errors.New("invalid setting value")

// Type classifies a tunable's value representation for the API client (the FE
// renders a duration picker vs a number input accordingly).
type Type string

const (
	// TypeDuration is a Go duration string (e.g. "15m0s", "2h0m0s").
	TypeDuration Type = "duration"
	// TypeInt is a base-10 integer.
	TypeInt Type = "int"
)

// The closed allowlist of runtime-tunable keys. These are the ONLY keys the
// settings API will read or write; anything else is ErrUnknownSetting.
const (
	// KeyDownloadInterval is the download ticker period (duration, >= 1m).
	KeyDownloadInterval = "jobs.download_interval"
	// KeyRefreshInterval is the discovery-sweep ticker period (duration, >= 10m).
	KeyRefreshInterval = "jobs.refresh_interval"
	// KeyRefreshConcurrency bounds parallel provider re-fetches (int, 1..32).
	KeyRefreshConcurrency = "jobs.refresh_concurrency"
	// KeyMaxRetries is the failed-download retry budget (int, 0..20).
	KeyMaxRetries = "jobs.max_retries"
	// KeyRetryBackoff is the base retry backoff delay (duration, >= 1s).
	KeyRetryBackoff = "jobs.retry_backoff"
	// KeyStaleGraceDays tunes the M7 source-health stale threshold (int, 0..365).
	KeyStaleGraceDays = "health.stale_grace_days"
)

// Defaults carries the config-resolved default for every tunable key. main
// builds it from *config.Config (see settings wiring in cmd/tsundoku) and injects
// it into NewService, so the settings layer never touches os.Getenv — the single
// env boundary stays in internal/config.
type Defaults struct {
	DownloadInterval   time.Duration
	RefreshInterval    time.Duration
	RefreshConcurrency int
	MaxRetries         int
	RetryBackoff       time.Duration
	StaleGraceDays     int
}

// tunable is one allowlisted key's metadata + validation. validate parses a raw
// value and checks its bounds, returning the canonical string to store (so the
// DB always holds a normalized, re-parseable value) or an ErrInvalidSetting. def
// returns the key's default as that same canonical string, sourced from the
// injected Defaults. One tunable is the SINGLE source of truth for a key, shared
// by the typed accessors, List, and Set (DRY — no validation logic is duplicated).
type tunable struct {
	key      string
	typ      Type
	unit     string
	validate func(raw string) (canonical string, err error)
	def      func(d Defaults) string
}

// tunableOrder fixes the stable order List returns the allowlist in (so the FE
// Settings screen renders deterministically and the drift gate stays stable).
var tunableOrder = []string{
	KeyDownloadInterval,
	KeyRefreshInterval,
	KeyRefreshConcurrency,
	KeyMaxRetries,
	KeyRetryBackoff,
	KeyStaleGraceDays,
}

// tunables is the key→tunable registry, built once from the bounds in the design
// spec's allowlist table. It is the closed set the API may touch.
var tunables = map[string]tunable{
	KeyDownloadInterval: durationTunable(
		KeyDownloadInterval, "duration", time.Minute,
		func(d Defaults) time.Duration { return d.DownloadInterval },
	),
	KeyRefreshInterval: durationTunable(
		KeyRefreshInterval, "duration", 10*time.Minute,
		func(d Defaults) time.Duration { return d.RefreshInterval },
	),
	KeyRefreshConcurrency: intTunable(
		KeyRefreshConcurrency, "count", 1, 32,
		func(d Defaults) int { return d.RefreshConcurrency },
	),
	KeyMaxRetries: intTunable(
		KeyMaxRetries, "count", 0, 20,
		func(d Defaults) int { return d.MaxRetries },
	),
	KeyRetryBackoff: durationTunable(
		KeyRetryBackoff, "duration", time.Second,
		func(d Defaults) time.Duration { return d.RetryBackoff },
	),
	KeyStaleGraceDays: intTunable(
		KeyStaleGraceDays, "days", 0, 365,
		func(d Defaults) int { return d.StaleGraceDays },
	),
}

// durationTunable builds a duration-typed tunable that rejects values below min
// and stores the canonical time.Duration string form.
func durationTunable(key, unit string, minVal time.Duration, def func(Defaults) time.Duration) tunable {
	return tunable{
		key:  key,
		typ:  TypeDuration,
		unit: unit,
		validate: func(raw string) (string, error) {
			d, err := time.ParseDuration(strings.TrimSpace(raw))
			if err != nil {
				return "", fmt.Errorf("%w: %s %q is not a valid duration", ErrInvalidSetting, key, raw)
			}
			if d < minVal {
				return "", fmt.Errorf("%w: %s must be at least %s (got %s)", ErrInvalidSetting, key, minVal, d)
			}
			return d.String(), nil
		},
		def: func(d Defaults) string { return def(d).String() },
	}
}

// intTunable builds an int-typed tunable that rejects values outside [lo,hi] and
// stores the canonical base-10 string form.
func intTunable(key, unit string, lo, hi int, def func(Defaults) int) tunable {
	return tunable{
		key:  key,
		typ:  TypeInt,
		unit: unit,
		validate: func(raw string) (string, error) {
			n, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil {
				return "", fmt.Errorf("%w: %s %q is not a valid integer", ErrInvalidSetting, key, raw)
			}
			if n < lo || n > hi {
				return "", fmt.Errorf("%w: %s must be in [%d, %d] (got %d)", ErrInvalidSetting, key, lo, hi, n)
			}
			return strconv.Itoa(n), nil
		},
		def: func(d Defaults) string { return strconv.Itoa(def(d)) },
	}
}
