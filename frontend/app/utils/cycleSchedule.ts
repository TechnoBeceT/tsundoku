/**
 * Cycle-schedule derivation — the pure kernel behind the Downloads header's two
 * background-loop pills (the download cycle and the discovery sweep).
 *
 * It turns one `CycleSchedule` from GET /api/engine/schedule plus the current
 * SERVER-corrected clock into a single state the UI can render honestly. No Vue,
 * no fetching: the composable owns the data, the components own the wording, and
 * this file owns the "what does the backend actually mean" decision so the two
 * pills can never disagree.
 *
 * THE ONE THING TO UNDERSTAND: `nextRunAt` is the EARLIEST instant the next cycle
 * may start — the previous cycle's START plus the configured interval, because the
 * interval is a true PERIOD, not a gap after the cycle ends. It can therefore be
 * ALREADY IN THE PAST while a cycle is still running: that is an overrunning cycle
 * (a 90s period with a 113s cycle), and at those settings back-to-back cycles with
 * zero idle time are the NORMAL steady state. The states below keep that case
 * readable instead of flashing a meaningless 0:00 countdown.
 */
import type { components } from '~/utils/api/schema.d.ts'

type CycleScheduleDTO = components['schemas']['CycleSchedule']

/**
 * The six honest states one background loop can be in.
 *
 *   running      a cycle is executing and the next one is not due yet;
 *   overrunning  a cycle is executing and the next one is ALREADY due — it will
 *                start the moment this one returns (back-to-back, zero idle);
 *   starting     no cycle is executing but the next is due — the loop is between
 *                cycles and about to run (a sub-second window on the server);
 *   waiting      no cycle is executing and the next one is in the future — the
 *                only state with a countdown;
 *   unscheduled  the loop is not scheduled at all (never started, or its context
 *                was cancelled) — `nextRunAt` is null. NOT the same as "late";
 *   unavailable  the schedule endpoint could not be read, so nothing is known.
 */
export type CycleState = 'running' | 'overrunning' | 'starting' | 'waiting' | 'unscheduled' | 'unavailable'

/** One loop's render-ready state: what it is doing, and (only when waiting) for how much longer. */
export interface CycleTimer {
  /** The loop's current state — see CycleState. */
  state: CycleState
  /**
   * Milliseconds until the next cycle may start. Non-null ONLY in the `waiting`
   * state: every other state either has no known next-run or has one that is
   * already due, and rendering "0:00" for those would read as a stuck clock.
   */
  remainingMs: number | null
}

/**
 * Derive one loop's render-ready state.
 *
 * @param cycle       the loop's slice of the last successful schedule read, or null
 *                    when the endpoint could not be read at all.
 * @param running     whether a cycle is executing right now. Passed in rather than
 *                    read off `cycle` because an SSE cycle.start / cycle.done edge
 *                    is fresher than the last fetched snapshot.
 * @param serverNowMs the current instant on the SERVER's clock (epoch ms) — the
 *                    browser clock corrected by the measured skew, never Date.now().
 *
 * The snapshot's own `overdue` flag is a point-in-time answer taken when the
 * response was built; it is deliberately re-derived here against the ticking
 * corrected clock so a cycle that overruns AFTER the fetch is still reported as
 * overrunning without another round trip. Both agree at fetch time by construction
 * (`overdue` is resolved against the same instant reported as `serverTime`).
 */
export function deriveCycleTimer(
  cycle: CycleScheduleDTO | null,
  running: boolean,
  serverNowMs: number,
): CycleTimer {
  if (cycle == null) return { state: 'unavailable', remainingMs: null }

  const nextRunAtMs = cycle.nextRunAt == null ? Number.NaN : Date.parse(cycle.nextRunAt)
  // No usable next-run instant: unscheduled. A running loop always publishes one,
  // so `running` here means the snapshot is simply older than the cycle start —
  // report the cycle, never the absence.
  if (Number.isNaN(nextRunAtMs)) {
    return { state: running ? 'running' : 'unscheduled', remainingMs: null }
  }

  const remainingMs = nextRunAtMs - serverNowMs
  const due = remainingMs <= 0

  if (running) return { state: due ? 'overrunning' : 'running', remainingMs: null }
  if (due) return { state: 'starting', remainingMs: null }
  return { state: 'waiting', remainingMs }
}

/**
 * Whether the state means work is happening or is about to — the pills render a
 * spinner for these and never a countdown.
 */
export function isCycleBusy(state: CycleState): boolean {
  return state === 'running' || state === 'overrunning' || state === 'starting'
}

/**
 * Whether the state carries no cadence information at all (`unscheduled` /
 * `unavailable`) — the pills render these muted, so "we don't know" never looks
 * like "nothing to do".
 */
export function isCycleUnknown(state: CycleState): boolean {
  return state === 'unscheduled' || state === 'unavailable'
}
