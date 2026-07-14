/**
 * trackerCallback — the sessionStorage handoff for the tracker OAuth round trip.
 *
 * The "Connect" click navigates the WHOLE TAB away to the tracker's own site
 * (spec `spec/trackers-oauth-phase3` §5 — a full-tab navigate, not a popup), and
 * the `state` param that comes back on `/auth/tracker/callback` is an OPAQUE
 * server-generated token (stashed server-side against the pending PKCE verifier
 * by `GET /api/trackers/{id}/auth-url`) — it does not carry the tracker id back
 * in a form this ONE SHARED callback route can read. `stashPendingTrackerId` is
 * called immediately before the navigate; `takePendingTrackerId` reads-and-clears
 * it on the callback page. sessionStorage is per-tab and survives a full
 * navigation within the SAME tab (a full-tab navigate — not a new tab/popup — so
 * this is exactly the tab that stashed it), and the one-shot clear means a stale
 * value can never satisfy a second/replayed visit to the callback URL.
 */
const PENDING_TRACKER_KEY = 'tsundoku.tracker.pendingId'

/** Stash the tracker id being connected, just before navigating away. */
export function stashPendingTrackerId(trackerId: number): void {
  sessionStorage.setItem(PENDING_TRACKER_KEY, String(trackerId))
}

/**
 * Reads and clears the stashed tracker id. Returns null when nothing was
 * stashed (or the stored value is somehow not a finite number) — the callback
 * page treats that as "we don't know which tracker this is" rather than
 * guessing.
 */
export function takePendingTrackerId(): number | null {
  const raw = sessionStorage.getItem(PENDING_TRACKER_KEY)
  sessionStorage.removeItem(PENDING_TRACKER_KEY)
  if (raw == null) return null
  const id = Number(raw)
  return Number.isFinite(id) ? id : null
}
