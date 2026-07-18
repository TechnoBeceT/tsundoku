/**
 * useNow — a module-singleton reactive clock.
 *
 * Returns a shared `now` ref (epoch ms) that ticks on a single interval, so every
 * "live" relative-time label across the app (e.g. the Downloads deferral "retry
 * ~Nm") stays current WITHOUT a refetch — a component just reads `now.value` in a
 * computed and it re-derives each tick. One interval backs all consumers (same
 * module-singleton shape as useProgressStream); it is created lazily on first use
 * and torn down when the last consumer's scope disposes.
 *
 * The 30s cadence matches the label granularity (minutes/hours) — a finer tick
 * would re-render for no visible change.
 */
import { ref, onScopeDispose } from 'vue'

// TICK_MS is the shared clock cadence; exported so a test can advance by exactly it.
export const TICK_MS = 30_000

const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | null = null
let subscribers = 0

export function useNow(): { now: typeof now } {
  subscribers++
  timer ??= setInterval(() => {
    now.value = Date.now()
  }, TICK_MS)
  // Tear the interval down once the last consumer goes away so it never leaks; a
  // later consumer re-arms it. Safe outside a component scope (onScopeDispose is a
  // no-op there), so a plain call in a util context still works.
  onScopeDispose(() => {
    subscribers--
    if (subscribers <= 0 && timer) {
      clearInterval(timer)
      timer = null
      subscribers = 0
    }
  })
  return { now }
}
