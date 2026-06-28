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
 *   cycle.start    | cycle.done
 *   refresh.start  | refresh.done
 *   health.summary  → payload { unhealthy: number }
 *
 * What each prop can and cannot drive:
 *   - `unhealthyCount`  ← health.summary payload { unhealthy } — exact, server-authoritative.
 *   - `syncing`         ← true on refresh.start, false on refresh.done — accurate for the
 *                          "Syncing sources…" header indicator.
 *   - `lastCycle`       ← 'start'/'done' on cycle.start/cycle.done — available for callers
 *                          that want to react to download-cycle boundaries.
 *   - activeDownloads / failedDownloads — the download.* events carry no running total in
 *     their payloads, so a reliable per-event count cannot be maintained here. Both remain
 *     at 0 in the layout; the Downloads screen (Milestone B) is the authoritative source.
 *
 * EventSource auto-reconnects on network loss; onerror sets connected=false but does NOT
 * tear down the source (the browser will retry automatically).
 */

const connected = ref(false)
const unhealthyCount = ref(0)
const syncing = ref(false)
const lastCycle = ref<'start' | 'done' | null>(null)

let source: EventSource | null = null
const listeners = new Map<string, Set<(data: unknown) => void>>()

const NAMED_EVENTS = [
  'download.start',
  'download.done',
  'download.fail',
  'download.skip',
  'cycle.start',
  'cycle.done',
  'refresh.start',
  'refresh.done',
  'health.summary',
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
        const data: unknown = JSON.parse(raw)

        if (name === 'health.summary' && typeof (data as { unhealthy?: unknown }).unhealthy === 'number') {
          unhealthyCount.value = (data as { unhealthy: number }).unhealthy
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

  return { connected, unhealthyCount, syncing, lastCycle, connect, disconnect, on }
}
