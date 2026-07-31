package imports

import (
	"context"
	"log/slog"
	"time"
)

// coverageFastPath is how long a request will WAIT for a fresh computation
// before answering `pending`. Short by design: it exists so a small series
// still feels synchronous (its whole walk takes well under a second), not to
// give a large one a chance to finish.
const coverageFastPath = 3 * time.Second

// coveragePendingStale is how long a `pending` row is trusted to mean "a walk
// is genuinely running right now" before a request is allowed to start a
// replacement.
//
// A bound is unavoidable: nothing rewrites the row when the process dies
// mid-walk, so a crash (or a container restart, or an engine-host kill)
// strands a `pending` snapshot that no later request would ever supersede,
// and the owner would sit on "Computing…" forever with no affordance to
// clear it.
//
// 30 minutes, because the bound has to sit ABOVE the slowest walk this
// feature was built for and as close above it as is defensible. The worked
// worst case is the one GAP-140 was opened on: ~1,301 chapters ≈ 330 WebView
// navigations ≈ 15-20 minutes. 30m clears that with real headroom, so a LIVE
// walk is never duplicated by its own slowness (the failure mode that would
// hurt most — two 20-minute walks hammering the source this feature exists to
// protect), while a walk killed by a restart self-heals within one owner
// sitting rather than needing a manual intervention that does not exist.
const coveragePendingStale = 30 * time.Minute

// coverageFailedCooldown is how long a `failed` row is served AS a failure
// before a request is allowed to retry the walk.
//
// A plain GET must not re-arm a walk on a fresh failure (GAP-140 final
// review, finding 1): a failing computation broadcasts imports.coverage.done,
// the scan-library screen re-fetches the breakdown on that event, and a
// re-fetch that recomputes produces another failure, another event, another
// re-fetch. That is a self-driving loop with no termination condition —
// measured at 3 full chapter walks from 3 GETs — and it hammers precisely the
// source this feature exists to be gentle with.
//
// A cooldown rather than "never" because the alternative bricks the pair: with
// no refresh affordance anywhere in the UI (a known, separately-tracked gap),
// a permanent `failed` row would mean one transient upstream blip costs the
// owner that series' coverage for good.
//
// 15 minutes, chosen against the loop rather than against user patience: the
// cooldown only has to outlast the round trip that closes the cycle
// (fail → SSE → re-fetch), which is near-instant, so any non-trivial value
// breaks it. 15m then bounds a persistently-broken source to at most 4 walks
// an hour even with a tab left open, which is the same politeness posture the
// download side takes, and it is short enough that an owner who fixes the
// underlying problem (restarts the engine, clears a ban) sees coverage return
// without wondering whether the app noticed.
const coverageFailedCooldown = 15 * time.Minute

// coverageMissingAfterCompute is the reason reported when a computation
// finished but left no row behind — see coverageAfterCompute.
const coverageMissingAfterCompute = "the coverage snapshot could not be stored"

// coverageNeedsCompute is the ADMISSION RULE: may this request start a
// chapter walk for a pair whose stored snapshot is (snap, ok)? force is the
// owner's explicit `?refresh=true` (the GAP-140 follow-up this function's
// history carries): before it existed, a `ready` snapshot could NEVER be
// recomputed by anyone — no UI action, no query param — so its counts froze
// at the first success forever, with only `computedAt` betraying the age.
//
// Pure, and deliberately separated from Coverage's I/O, because the two
// timeouts it weighs are far too long to drive through a real walk in a test
// — a table test can hand it a synthetic `now` instead.
//
//   - never computed (ok == false) → YES, regardless of force. Nothing exists
//     to serve.
//   - a LIVE `pending` row (younger than coveragePendingStale) → NO, EVEN WHEN
//     force is set — this is the ONE guard force cannot bypass, checked
//     BEFORE the force branch below for exactly that reason. A pending row
//     means a walk is already running, and a refresh arriving mid-walk must
//     JOIN that walk (served as `pending`) rather than start a second
//     ~20-minute WebView walk against the source this feature exists to
//     protect. force answers "may I force a NEW computation", not "may I
//     have two running at once" — those are different questions, and only
//     the first is what `?refresh=true` asks.
//   - force == true (and the row above did not already say no) → YES. The
//     explicit owner override: it bypasses the `ready` short-circuit and the
//     `failed` cooldown alike, because the owner asked for a fresh
//     computation right now, not "whenever policy would otherwise allow it".
//   - ready → NO. Serving the persisted snapshot with its as-of is the entire
//     point of GAP-140; one completed walk makes every later view free unless
//     the owner explicitly asks for a fresh one (see force above).
//   - pending → NO while the row is younger than coveragePendingStale — the
//     SAME guard as the live-pending bullet above (reaching this switch arm at
//     all already means the row is stale). Older than the bound, the claim is
//     treated as a lie left by a dead process and the walk is restarted,
//     force or not.
//   - failed → NO while the row is younger than coverageFailedCooldown, which
//     is what breaks the fail → announce → re-fetch → fail loop above, unless
//     force overrides it.
//   - anything else (a status a future migration introduces, say) → YES, the
//     fail-safe direction: recomputing is wasteful, serving an uninterpretable
//     row as authoritative is wrong.
//
// KNOWN RESIDUAL WINDOW: this is a read-then-write, not a lock. Two requests
// that BOTH load the store before either marks the row pending will both
// start a walk. That window is the width of one round trip to Postgres, and
// none of the collisions this rule was written for sit inside it — the tabs,
// reloads and SSE re-fetches are all separated by orders of magnitude more.
// The duplicate is also harmless rather than corrupting: the UNIQUE(source_id,
// manga_url) index means the second walk overwrites the first's row with an
// equivalent one. Closing it properly needs a conditional write (or the
// per-series latch pattern internal/library uses), which is not worth the
// mechanism at this width. force does not widen this window: it is still one
// read (this function) then one write (markCoveragePending), same as before.
func coverageNeedsCompute(snap CoverageSnapshot, ok bool, now time.Time, force bool) bool {
	if !ok {
		return true
	}
	// A live pending walk is never duplicated — not even by an explicit
	// refresh. Checked before the force branch so force has no path around it.
	if snap.Status == coverageStatusPending && now.Sub(snap.UpdatedAt) < coveragePendingStale {
		return false
	}
	if force {
		return true
	}
	switch snap.Status {
	case coverageStatusReady:
		return false
	case coverageStatusPending:
		// Reaching here means the row already failed the live-pending check
		// above, i.e. it is stale — always restarted.
		return true
	case coverageStatusFailed:
		return now.Sub(snap.UpdatedAt) >= coverageFailedCooldown
	default:
		return true
	}
}

// coverageAfterCompute renders what Coverage returns once a computation it
// started has finished and the store has been re-read as (snap, ok).
//
// It exists to honour `ok`. Every exit path of ComputeCoverage persists a row,
// so ok == false here means the store write itself failed (the mark-pending
// upsert erroring while reads still succeed) — and the zero CoverageSnapshot
// that case used to be returned verbatim carries Status "", which is not a
// member of the wire enum. It reached the client as {"status":""}, which the
// scan-library row renders as NOTHING: no counts, no "Computing…", no
// "unavailable" (GAP-140 final review, finding 4).
//
// `failed` rather than `pending` is the honest rendering: the computation is
// over and produced nothing readable, so a `pending` here would spin the UI on
// a walk that will never report again.
func coverageAfterCompute(snap CoverageSnapshot, ok bool) CoverageSnapshot {
	if !ok {
		return CoverageSnapshot{Status: coverageStatusFailed, LastError: coverageMissingAfterCompute}
	}
	return snap
}

// Coverage returns the per-scanlator breakdown for (sourceID, url), the
// entry point GAP-140 gives the HTTP handler in place of the old unbounded
// SourceBreakdown walk.
//
// sourceID is resolved FIRST, synchronously, before touching the coverage
// store at all: resolveSource is one fast in-memory list check (a single
// client.Sources call, no WebView walk), so an unknown source is a genuine
// client error and gets an immediate ErrSourceNotFound (→ 404 at the
// handler), exactly like Details/Browse — not a 3s wait followed by a
// persisted `failed` snapshot. This also means no SourceCoverage row is EVER
// written for a source id that does not exist: Rule 2 (never-auto-delete)
// gives such a row no cleanup path, so a mistyped or stale sourceId must
// never be allowed to create one in the first place.
//
// WHETHER a walk starts at all is coverageNeedsCompute's decision, and that
// doc comment is the authoritative statement of the rule — read it before
// changing anything here. In short: a plain GET serves a `ready` snapshot, a
// live `pending` one and a recently-`failed` one as they stand, and starts a
// walk only for a pair that was never computed, a `pending` row old enough to
// be a dead process's leftover, or a failure past its cooldown.
//
// refresh is the caller's `?refresh=true` — an explicit request to force a
// recomputation. It bypasses the `ready` short-circuit and the `failed`
// cooldown, but NEVER a live `pending` walk: coverageNeedsCompute treats a
// walk already in flight as un-duplicable regardless of refresh, so a refresh
// arriving mid-walk is served the same `pending` body a plain GET would get.
//
// When a walk IS started it runs on a DETACHED context (context.WithoutCancel
// — the walk must NOT die the instant this request's own context is torn
// down, which is exactly why a slow computation used to be unrecoverable) and
// the caller waits at most coverageFastPath for it. Small series therefore
// behave exactly as before (their whole walk finishes well inside the
// window); only expensive ones fall through to `pending`, with
// imports.coverage.done delivering the result when it lands.
//
// A ComputeCoverage failure (once the source itself is known-good — an
// upstream fetch failure, say) is deliberately NOT surfaced as an error
// return here: every exit path of ComputeCoverage already persists a
// `failed` snapshot with its reason and announces imports.coverage.done (see
// its doc comment), so a caller sees the failure as an ordinary
// CoverageSnapshot with Status == coverageStatusFailed, not as an HTTP-level
// error. The error this function DOES return (besides ErrSourceNotFound) is
// reserved for a genuine store failure — loadCoverage itself unable to read
// the store.
func (s *Service) Coverage(ctx context.Context, sourceID, url, mangaTitle string, refresh bool) (CoverageSnapshot, error) {
	if _, err := s.resolveSource(ctx, sourceID); err != nil {
		return CoverageSnapshot{}, err
	}

	snap, ok, err := s.loadCoverage(ctx, sourceID, url)
	if err != nil {
		return CoverageSnapshot{}, err
	}
	if !coverageNeedsCompute(snap, ok, time.Now().UTC(), refresh) {
		return snap, nil
	}

	done := make(chan struct{})
	// context.WithoutCancel: the computation must NOT die when this request
	// returns, which is the entire reason a slow walk was unrecoverable before
	// (GAP-140) — a plain ctx here would cancel the walk the instant the HTTP
	// handler's request context is torn down, right after the fast-path select
	// below falls through to `pending`.
	bg := context.WithoutCancel(ctx)
	go func() {
		defer close(done)
		if err := s.ComputeCoverage(bg, sourceID, url, mangaTitle); err != nil {
			slog.WarnContext(bg, "imports.Coverage: background computation failed",
				"source_id", sourceID, "manga_url", url, "err", err)
		}
	}()

	select {
	case <-done:
		// bg, NOT ctx: the walk just finished on the detached context, and if
		// the ORIGINAL request's ctx was already cancelled (e.g. the client
		// disconnected) right before this branch fires, re-reading with ctx
		// would fail on the very row ComputeCoverage just wrote successfully —
		// the same class of bug WithoutCancel exists to prevent, on the read
		// side instead of the write side.
		fresh, freshOK, err := s.loadCoverage(bg, sourceID, url)
		if err != nil {
			return CoverageSnapshot{}, err
		}
		return coverageAfterCompute(fresh, freshOK), nil
	case <-time.After(coverageFastPath):
		return CoverageSnapshot{Status: coverageStatusPending}, nil
	}
}
