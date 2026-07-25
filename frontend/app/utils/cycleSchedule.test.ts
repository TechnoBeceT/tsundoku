/**
 * deriveCycleTimer — the full state matrix of one background loop, pinned.
 *
 * Every state the GET /api/engine/schedule contract can express gets a case:
 * running · overrunning (a cycle running past its own next-due instant) · starting
 * (due, between cycles) · waiting (the only state with a countdown) · unscheduled
 * (nextRunAt null) · unavailable (no snapshot at all).
 *
 * Non-vacuous: collapse `overrunning` back into `running` and the back-to-back case
 * fails; take the countdown from the raw browser clock instead of the passed
 * server-corrected instant and the skew case fails.
 */
import { describe, it, expect } from 'vitest'
import { deriveCycleTimer, isCycleBusy, isCycleUnknown } from './cycleSchedule'
import type { CycleState } from './cycleSchedule'

const T0 = Date.parse('2026-07-25T12:00:00Z')
const at = (offsetMs: number): string => new Date(T0 + offsetMs).toISOString()

describe('deriveCycleTimer', () => {
  it('reports a cycle running on time, with no countdown', () => {
    // Running, next due in 47s — the healthy in-flight case.
    const timer = deriveCycleTimer({ running: true, nextRunAt: at(47_000), overdue: false }, true, T0)
    expect(timer).toEqual({ state: 'running', remainingMs: null })
  })

  it('reports an OVERRUNNING cycle when the next run is already due', () => {
    // The owner's live shape: a 90s period with a 113s cycle, so nextRunAt (start
    // + 90s) passed 23s ago while the cycle is still going.
    const timer = deriveCycleTimer({ running: true, nextRunAt: at(-23_000), overdue: true }, true, T0)
    expect(timer).toEqual({ state: 'overrunning', remainingMs: null })
  })

  it('re-derives overdue against the ticking clock, not the snapshot flag', () => {
    // Snapshot said "not overdue" when it was built; 10s later the instant has
    // passed. No refetch is needed for the pill to tell the truth.
    const cycle = { running: true, nextRunAt: at(5_000), overdue: false }
    expect(deriveCycleTimer(cycle, true, T0).state).toBe('running')
    expect(deriveCycleTimer(cycle, true, T0 + 10_000).state).toBe('overrunning')
  })

  it('reports STARTING when the next run is due but no cycle is executing', () => {
    const timer = deriveCycleTimer({ running: false, nextRunAt: at(-200), overdue: true }, false, T0)
    expect(timer).toEqual({ state: 'starting', remainingMs: null })
  })

  it('reports WAITING with the remaining milliseconds', () => {
    const timer = deriveCycleTimer({ running: false, nextRunAt: at(43_000), overdue: false }, false, T0)
    expect(timer).toEqual({ state: 'waiting', remainingMs: 43_000 })
  })

  it('measures the countdown against the SERVER clock it is handed', () => {
    const cycle = { running: false, nextRunAt: at(60_000), overdue: false }
    // A browser clock running 30s fast would show 0:30; corrected, it is 1:00.
    expect(deriveCycleTimer(cycle, false, T0).remainingMs).toBe(60_000)
    expect(deriveCycleTimer(cycle, false, T0 + 30_000).remainingMs).toBe(30_000)
  })

  it('reports UNSCHEDULED when nextRunAt is null — never "late"', () => {
    const timer = deriveCycleTimer({ running: false, nextRunAt: null, overdue: false }, false, T0)
    expect(timer).toEqual({ state: 'unscheduled', remainingMs: null })
  })

  it('still reports a running cycle when the snapshot has no next-run instant', () => {
    // Defensive: the loop always publishes a next-run while running, so this only
    // happens with a snapshot older than the cycle start. Report the cycle.
    const timer = deriveCycleTimer({ running: false, nextRunAt: null, overdue: false }, true, T0)
    expect(timer).toEqual({ state: 'running', remainingMs: null })
  })

  it('treats an unparseable timestamp as no known next-run', () => {
    const timer = deriveCycleTimer({ running: false, nextRunAt: 'not-a-date', overdue: false }, false, T0)
    expect(timer).toEqual({ state: 'unscheduled', remainingMs: null })
  })

  it('reports UNAVAILABLE when there is no snapshot', () => {
    expect(deriveCycleTimer(null, false, T0)).toEqual({ state: 'unavailable', remainingMs: null })
    // Even a remembered running flag cannot make an unreadable schedule knowable.
    expect(deriveCycleTimer(null, true, T0)).toEqual({ state: 'unavailable', remainingMs: null })
  })
})

describe('isCycleBusy / isCycleUnknown', () => {
  const busy: CycleState[] = ['running', 'overrunning', 'starting']
  const idle: CycleState[] = ['waiting', 'unscheduled', 'unavailable']
  const unknown: CycleState[] = ['unscheduled', 'unavailable']
  const known: CycleState[] = ['running', 'overrunning', 'starting', 'waiting']

  it('classifies the busy states (spinner) and the unknown states (muted)', () => {
    expect(busy.every(isCycleBusy)).toBe(true)
    expect(idle.some(isCycleBusy)).toBe(false)
    expect(unknown.every(isCycleUnknown)).toBe(true)
    expect(known.some(isCycleUnknown)).toBe(false)
  })
})
