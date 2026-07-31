import { ref } from 'vue'

/**
 * useProgressStream — module-singleton wrapping a native EventSource('/api/progress').
 *
 * State and the EventSource live at module scope so every component shares the
 * same reactive refs (same pattern as useAuth). `connect()` is idempotent — a
 * second call while the source is open is a no-op.
 *
 * SSE event names (verbatim from the backend):
 *   download.start | download.done | download.fail | download.skip
 *   download.progress → payload { chapter_id, current, total, state } — live
 *     per-page progress during a download/upgrade fetch. Forwarded raw via `on()`
 *     (no hub-level interpretation); useDownloads maps it to the row's % + counter.
 *   cycle.start    | cycle.done
 *   refresh.start  | refresh.done
 *   health.summary  → payload { unhealthy: number } — SERIES-health signal (stale
 *     sync), refreshed on the 2h sweep. Drives the amber "N need attention" pill.
 *   sources.summary → payload { erroring: number, coolingDown: number } — SOURCE
 *     signal, pushed IMMEDIATELY on a circuit-breaker trip/clear (and re-emitted on
 *     each refresh tick, belt-and-braces). `erroring` = sources in a failure streak
 *     right now; `coolingDown` = sources whose breaker is tripped and still in
 *     cooldown. Drives the DANGER Health-rail badge — the "a source broke, I need to
 *     KNOW" alert, distinct from and complementary to health.summary.
 *   chapter.new     → payload { groups:[{seriesId,title,count,url}], total, digest,
 *     title, body } — one or more armed monitored series gained new readable
 *     chapters this cycle. Forwarded raw via `on()`; the default layout renders it
 *     as an in-app toast (only when the tab is visible — the service worker's Web
 *     Push handler shows the OS notification when it is not).
 *   provider.merged → payload { seriesId, error? } — an async Match/merge
 *     (StartMatchDiskProvider) finished for that series. Forwarded raw via `on()`;
 *     useSeriesDetail listens for its own series id to clear the "matching…" state
 *     and refetch (or surface `error` on failure). Fires on success AND failure.
 *   library.dedup.done → payload { seriesProcessed, merged, skipped, busy, error? }
 *     — TERMINAL summary of the library-wide duplicate-source sweep (POST
 *     /api/library/dedup-providers). That endpoint answers a bare 202 and runs
 *     detached, so this event is the ONLY channel its outcome ever reaches the
 *     client on. Forwarded raw via `on()`; useLibraryMaintenance parses it
 *     (utils/dedupSweepSummary) into the Settings dialog's outcome lines.
 *   scan.start | scan.progress | scan.done — the Library-Import scan (see
 *     useScanLibrary): scan.start carries no payload; scan.progress carries
 *     { processed, total, path }; scan.done is TERMINAL and carries either
 *     { total, found } on success or { error } if the walk failed/timed out.
 *     Forwarded raw via `on()` — this composable does not interpret them
 *     (unlike health.summary / refresh.start / cycle.start) because the
 *     terminal-latch logic (ignore a late scan.progress after scan.done) is
 *     a scan-specific concern that belongs in useScanLibrary, not in this
 *     shared hub.
 *   imports.coverage.done → payload { sourceId, mangaUrl, status, total, error? }
 *     — TERMINAL report of one background per-scanlator coverage computation
 *     (GAP-140). The endpoint that starts a slow walk returns before it
 *     finishes, so this is the only channel the eventual outcome reaches the
 *     client on. Forwarded raw via `on()`; useScanLibrary matches it against
 *     its breakdown cache by (source, url) and re-fetches that entry.
 *
 * What each prop can and cannot drive:
 *   - `unhealthyCount`   ← health.summary payload { unhealthy } — exact, server-authoritative.
 *   - `erroringSources`  ← sources.summary payload { erroring } — exact, server-authoritative.
 *   - `coolingDownSources` ← sources.summary payload { coolingDown } — exact, server-authoritative.
 *   - `syncing`         ← true on refresh.start, false on refresh.done. EDGE-ONLY, with the
 *                          blind spot every edge-only flag has (see below): a tab that
 *                          connects while a sweep is already running shows nothing until the
 *                          next sweep. Adequate for the shell's transient "Syncing sources…"
 *                          hint; NOT adequate for a claim about the engine's current state.
 *   - `lastCycle`       ← 'start'/'done' on cycle.start/cycle.done — available for callers
 *                          that want to react to download-cycle boundaries.
 *   - activeDownloads / failedDownloads — the download.* events carry no running total in
 *     their payloads, so a reliable per-event count cannot be maintained here. Both remain
 *     at 0 in the layout; the Downloads screen (Milestone B) is the authoritative source.
 *
 * WHAT THIS STREAM CANNOT TELL YOU (GAP-115). Every flag here is derived purely from
 * EDGES, so it only knows what happened while this tab was listening. A page loaded
 * mid-cycle has seen no `cycle.start` and would render "Idle" beside a source strip
 * that is visibly downloading — which is exactly the contradiction the owner hit. A
 * running/next-run claim therefore belongs to `useCycleTimers`, which reads
 * GET /api/engine/schedule for the truth and uses these events only as the liveness
 * signal that it is time to re-read. This composable deliberately no longer exposes a
 * `cycleActive` flag: keeping one invited exactly that mistake.
 *
 * EventSource auto-reconnects on network loss; onerror sets connected=false but does NOT
 * tear down the source (the browser will retry automatically).
 */

const connected = ref(false)
const unhealthyCount = ref(0)
const erroringSources = ref(0)
const coolingDownSources = ref(0)
const syncing = ref(false)
const lastCycle = ref<'start' | 'done' | null>(null)

let source: EventSource | null = null
const listeners = new Map<string, Set<(data: unknown) => void>>()

const NAMED_EVENTS = [
  'download.start',
  'download.done',
  'download.fail',
  'download.skip',
  'download.progress',
  'cycle.start',
  'cycle.done',
  'refresh.start',
  'refresh.done',
  'health.summary',
  'sources.summary',
  'extensions.checked',
  'chapter.new',
  'provider.merged',
  'scan.start',
  'scan.progress',
  'scan.done',
  'library.dedup.done',
  'imports.coverage.done',
] as const

function emit(event: string, data: unknown): void {
  listeners.get(event)?.forEach((cb) => cb(data))
}

export function useProgressStream() {
  function connect(): void {
    if (source) return
    source = new EventSource('/api/progress')

    source.onopen = () => { connected.value = true }
    // EventSource auto-reconnects; mark disconnected so callers can react, but
    // do NOT close the source — the browser will retry automatically.
    source.onerror = () => { connected.value = false }

    for (const name of NAMED_EVENTS) {
      source.addEventListener(name, (ev) => {
        // The browser wraps all named SSE events in MessageEvent. The EventSource
        // DOM types only declare 'error'|'message'|'open', so named events fall
        // through to the generic Event overload and must be asserted. The string
        // generic makes .data typed as string (not any) to satisfy strict rules.
        const raw: string = (ev as MessageEvent<string>).data
        let data: unknown
        try {
          data = JSON.parse(raw)
        } catch {
          console.warn('[useProgressStream] unparseable SSE payload on', name, raw)
          return
        }

        if (name === 'health.summary' && typeof (data as { unhealthy?: unknown }).unhealthy === 'number') {
          unhealthyCount.value = (data as { unhealthy: number }).unhealthy
        }
        if (name === 'sources.summary') {
          const p = data as { erroring?: unknown, coolingDown?: unknown }
          if (typeof p.erroring === 'number') erroringSources.value = p.erroring
          if (typeof p.coolingDown === 'number') coolingDownSources.value = p.coolingDown
        }
        if (name === 'refresh.start') syncing.value = true
        if (name === 'refresh.done') syncing.value = false
        if (name === 'cycle.start') lastCycle.value = 'start'
        if (name === 'cycle.done') lastCycle.value = 'done'

        emit(name, data)
      })
    }
  }

  function disconnect(): void {
    source?.close()
    source = null
    connected.value = false
  }

  /**
   * Subscribe to a named SSE event. Returns an unsubscribe function.
   * The callback receives the JSON-parsed event data.
   */
  function on(event: string, cb: (data: unknown) => void): () => void {
    if (!listeners.has(event)) listeners.set(event, new Set())
    listeners.get(event)!.add(cb)
    return () => listeners.get(event)?.delete(cb)
  }

  return { connected, unhealthyCount, erroringSources, coolingDownSources, syncing, lastCycle, connect, disconnect, on }
}
