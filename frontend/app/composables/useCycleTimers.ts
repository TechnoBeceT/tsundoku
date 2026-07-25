/**
 * useCycleTimers — the Downloads header's view of the two background loops: the
 * download cycle and the discovery sweep.
 *
 * THE BACKEND IS THE AUTHORITY. Everything rendered here comes from
 * GET /api/engine/schedule (`running`, `nextRunAt`, `overdue`, `serverTime`) — a
 * pure in-memory read of the job runner's own snapshot, no DB and no engine calls.
 * The client never invents a schedule of its own.
 *
 * WHY IT IS BUILT THIS WAY (GAP-115). The previous version derived both countdowns
 * client-side from `GET /api/settings` + the SSE boundaries: it seeded "next fire =
 * now + interval" on every mount and re-seeded on `cycle.done`. Three things were
 * wrong with that. Every page reload restarted the countdowns at a full interval,
 * because a fresh tab knows nothing about the running server. The re-seed encoded a
 * post-cycle GAP, but the interval is a true PERIOD measured from the previous
 * cycle's START, so the countdown was off by a whole cycle duration — worst exactly
 * when cycles overrun. And a tab that connected mid-cycle saw no `cycle.start`, so
 * the header claimed "Idle" while sources were visibly downloading beside it.
 *
 * THE MODEL NOW:
 *   - fetch the schedule on mount (this is what makes a mid-cycle reload correct),
 *     and again on every SSE cycle/refresh boundary and on SSE reconnect;
 *   - SSE is the LIVENESS signal, not the state: a `cycle.start` / `cycle.done`
 *     edge flips `running` instantly, and the fetch it triggers brings the fresh
 *     `nextRunAt` (which is re-anchored on each cycle START, so refetching on start
 *     as well as on done is required — without it, a snapshot taken before the
 *     cycle began makes every running cycle look overrun);
 *   - a local 1-second ticker only drives the seconds-precision display. There is
 *     no polling loop against the endpoint.
 *
 * CLOCK SKEW. Countdowns are measured against the SERVER's clock, never the
 * browser's. Each fetch samples the skew as `(sent + received) / 2 - serverTime`
 * (the request midpoint, so the round trip is split rather than charged wholly to
 * one side) and every subsequent tick is corrected by it. A browser clock minutes
 * off therefore cannot produce a wrong or negative countdown.
 *
 * BACK-TO-BACK CYCLES ARE A NORMAL STEADY STATE. At the owner's settings (a 90s
 * period, ~113s cycles) the next run is due before the current one finishes and
 * cycles run continuously with zero idle time. That surfaces as the `overrunning`
 * state, which carries no countdown at all — the header reads "running, next due
 * now" instead of pinning a misleading 0:00 clock or flapping every second.
 *
 * `jobs.download_interval` is no longer read here for anything: the period is the
 * server's business, and re-deriving it client-side is the bug this replaced.
 *
 * Returns one `CycleTimer` per loop (state + remaining ms); the presentational
 * CycleTimers.vue / CycleBanner.vue only format them.
 */
import { ref, computed, watch, onScopeDispose } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import { deriveCycleTimer } from '~/utils/cycleSchedule'
import { useProgressStream } from './useProgressStream'

type EngineScheduleDTO = components['schemas']['EngineSchedule']

/** Cadence of the local clock that drives the seconds-precision countdown. */
const TICK_MS = 1000

/**
 * One loop's live running flag, with the two freshness guards that keep an SSE
 * edge and an in-flight fetch from overwriting each other:
 *
 *   - `mark` records an SSE boundary. Edges are always the newest truth, so they
 *     win outright and bump the edge counter.
 *   - `applyFetched` writes a fetched flag only when no edge landed since the
 *     fetch started, and only when the caller asked to re-seed at all. A fetch
 *     triggered BY an edge must not re-seed: the server publishes its own
 *     running flag around the SSE emission, so that response can legitimately
 *     still describe the state the edge just superseded.
 */
function createLoopLiveness() {
  const running = ref(false)
  let edges = 0
  return {
    running,
    /** The edge counter to hand back to applyFetched after an await. */
    edge: (): number => edges,
    /** Apply an SSE boundary: authoritative, and it invalidates in-flight fetches. */
    mark(value: boolean): void {
      edges += 1
      running.value = value
    },
    /** Seed from a fetched snapshot unless an SSE edge landed while it was in flight. */
    applyFetched(value: boolean, seenEdges: number): void {
      if (edges === seenEdges) running.value = value
    },
  }
}

export function useCycleTimers() {
  const stream = useProgressStream()
  stream.connect()

  // The last SUCCESSFUL schedule read. Null means "not known" and renders as the
  // `unavailable` state: a failed read drops the snapshot rather than keeping a
  // countdown ticking against a server that may have restarted and re-anchored its
  // loops — a stale countdown is exactly the invented truth this composable exists
  // to remove. Recovery is free: the SSE reconnect below refetches.
  const schedule = ref<EngineScheduleDTO | null>(null)

  // Browser clock minus server clock, in ms. Re-sampled on every successful read.
  const skewMs = ref(0)

  const now = ref(Date.now())
  const ticker = setInterval(() => { now.value = Date.now() }, TICK_MS)

  /** The current instant on the SERVER's clock — the reference for every countdown. */
  const serverNow = computed(() => now.value - skewMs.value)

  const downloadLoop = createLoopLiveness()
  const refreshLoop = createLoopLiveness()

  // Only the newest read may write: a slow response must never overwrite a fresher one.
  let fetchSeq = 0

  /**
   * Read the schedule endpoint and apply it.
   *
   * @param seedRunning whether this read may (re-)seed the running flags. True for
   *   the mount and reconnect reads, which are the only ones that know more than
   *   the SSE stream; false for reads triggered by an SSE boundary, whose response
   *   may pre-date the edge that triggered it.
   *
   * Failures are not surfaced as a banner — this is passive header state, not a
   * user action — but they are not swallowed either: the pills switch to the
   * explicit "schedule unavailable" state rather than showing a fabricated one.
   */
  async function load(seedRunning: boolean): Promise<void> {
    const seq = ++fetchSeq
    const downloadEdge = downloadLoop.edge()
    const refreshEdge = refreshLoop.edge()
    const sentAt = Date.now()

    let data: EngineScheduleDTO | null = null
    try {
      const res = await apiClient.GET('/api/engine/schedule')
      if (!res.error && res.data) data = res.data
    }
    catch {
      data = null
    }

    if (seq !== fetchSeq) return // a newer read already landed
    schedule.value = data
    if (data == null) return

    // Split the round trip between the two clocks, then correct every later tick by it.
    const serverTimeMs = Date.parse(data.serverTime)
    if (!Number.isNaN(serverTimeMs)) {
      skewMs.value = (sentAt + Date.now()) / 2 - serverTimeMs
    }
    if (seedRunning) {
      downloadLoop.applyFetched(data.download.running, downloadEdge)
      refreshLoop.applyFetched(data.refresh.running, refreshEdge)
    }
  }

  // SSE boundaries: flip the running flag instantly, then refresh the instants.
  const unsubscribe = [
    stream.on('cycle.start', () => { downloadLoop.mark(true); void load(false) }),
    stream.on('cycle.done', () => { downloadLoop.mark(false); void load(false) }),
    stream.on('refresh.start', () => { refreshLoop.mark(true); void load(false) }),
    stream.on('refresh.done', () => { refreshLoop.mark(false); void load(false) }),
  ]

  // A reconnect means the tab may have missed whole cycles (or the server
  // restarted): re-seed everything from the endpoint, exactly like a fresh mount.
  const stopWatch = watch(stream.connected, (isConnected) => {
    if (isConnected) void load(true)
  })

  onScopeDispose(() => {
    clearInterval(ticker)
    unsubscribe.forEach((off) => off())
    stopWatch()
  })

  void load(true)

  return {
    /** The download-cycle loop's live state (state + remaining ms). */
    download: computed(() => deriveCycleTimer(schedule.value?.download ?? null, downloadLoop.running.value, serverNow.value)),
    /** The discovery-sweep loop's live state (state + remaining ms). */
    refresh: computed(() => deriveCycleTimer(schedule.value?.refresh ?? null, refreshLoop.running.value, serverNow.value)),
  }
}
