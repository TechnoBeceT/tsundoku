/**
 * useScanLibrary — data layer for the Scan Library wizard (migrating an
 * existing on-disk manga library into Tsundoku without re-downloading).
 *
 * Endpoints consumed:
 *   POST /api/library/scan            — launch the async background scan
 *   GET  /api/library/imports         — paginated staged-entry list (?status=&limit=&offset=)
 *   GET  /api/library/imports/match   — cross-source search candidates for one staged entry
 *   POST /api/library/import          — import one staged entry (disk-only, or with a match)
 *   POST /api/library/imports/skip    — mark a staged entry skipped (no disk I/O)
 *   POST /api/library/import/batch    — bulk disk-only import of many staged entries
 *
 * `GET /api/library/imports/match` returns the IDENTICAL SearchGroup/
 * SearchCandidate DTO as `GET /api/search` (the Adopt wizard) — the DTO →
 * screen-type mapping is shared via `importMappers.ts` (§2 DRY; see
 * `useImport.ts` for the other consumer).
 *
 * SSE terminal-latch (Task-3 concurrency review, binding): `scan.done` is
 * TERMINAL. On a scan timeout the backend's leaked walk goroutine can still
 * emit a `scan.progress` frame AFTER the terminal `scan.done` — this
 * composable only accepts `scan.progress` while `scanState.status ===
 * 'scanning'`, so a late frame can never flip status back from 'done'.
 * `scan.done`'s `error` field (if present) is carried onto `scanState.error`
 * so the wizard can render "scan timed out / failed" (§16 — no silent
 * failure). `startScan()` treats a 409 `{started:false}` (a scan already in
 * flight — the owner double-clicked, or the wizard reopened mid-scan) as
 * "already scanning", not an error.
 *
 * Pagination mirrors `useLibrary.ts`'s offset/`loadMore()` pattern, except
 * `GET /api/library/imports` carries no `X-Total-Count` header — `hasMore`
 * is therefore the classic "the page came back full" heuristic (a full page
 * MAY mean more rows exist; a short page never does).
 *
 * Per-row mutations (`skip`/`importDiskOnly`/`importWithMatches`) follow the
 * shared `mutate(busyId, fn)` convention from `useCategories.ts`: set busy,
 * run the call, refetch the list on success, clear busy. Unlike
 * `useCategories` (whose UI only ever shows one row's error, e.g. inside a
 * modal), the Scan-Library table can show MANY rows with independent errors
 * at once, so busy/error state is tracked per-path (a `Set`/`Record`) rather
 * than a single shared field — exposed as the `busy(path)`/`error(path)`
 * lookup functions the wizard screen (Task 6/7) will call per row.
 *
 * `match()`'s result state (`matchGroups`/`matching`/`matchError`) lives HERE
 * rather than in the page (Task-7 review fix) precisely so it can carry a
 * stale-response guard: the owner can open the Match panel for one staged
 * entry, go Back, and open it for another before the first search resolves —
 * without a guard the slower, superseded request's candidates would land
 * after the faster one's and silently overwrite the panel the owner is
 * actually looking at. A monotonic generation counter (incremented per
 * `match()` call) ensures only the MOST RECENTLY STARTED request's response
 * is ever written to the shared refs, regardless of resolution order.
 *
 * `breakdowns`/`loadBreakdowns` (Slice P) are copied verbatim from
 * `useMatchSource.loadBreakdowns` (§2 DRY — identical cache/in-flight-guard/
 * parallel-fetch shape): per-scanlator chapter-coverage for the Match panel's
 * Configure-stage auto-split, keyed `source:mangaId`, `null` = fetch failed,
 * an absent key = not yet attempted.
 *
 * `importWithMatches` (Slice P, was `importWithMatch`) POSTs `{path, matches}`
 * — a `ProviderRef[]` gathered via the shared `useSourceConfigure` Configure
 * stage, replacing the old single `{path, match: {source, mangaId,
 * importance}}` (a fixed importance of 2). The new `ProviderRef[]` carries no
 * importance: the backend assigns each provider an importance strictly below
 * the disk-origin provider's (decision E), in list order. An empty `matches`
 * array is a valid disk-only import (mirrors `importDiskOnly`).
 */
import { ref, onUnmounted } from 'vue'
import { useProgressStream } from '~/composables/useProgressStream'
import { mapGroup, mapScanlatorCoverage } from '~/composables/importMappers'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { ProviderRef } from '~/composables/useSourceConfigure'
import type { ScanlatorCoverage, SearchCandidate, SearchGroup } from '~/components/screens/import.types'

type FoundSeriesDTO = components['schemas']['FoundSeries']
type BatchImportFailureDTO = components['schemas']['BatchImportFailure']

/** Stable cache/in-flight key for one (source, mangaId) breakdown fetch (mirrors `useMatchSource`). */
function breakdownKey(source: string, mangaId: number): string {
  return `${source}:${mangaId}`
}

/** Page size for the staged-entries list (mirrors useLibrary's PAGE). */
const PAGE = 50

/** The three staging statuses a scan entry can be filtered to; null = all. */
export type ScanStatusFilter = 'pending' | 'imported' | 'skipped' | null

/** One staged library-scan entry, mapped from the backend's FoundSeries DTO. */
export interface ScanEntry {
  /** Absolute on-disk path to the series folder — the entry's identity key. */
  path: string
  /** Series title as read from disk (ComicInfo.xml/sidecar or folder name). */
  title: string
  /** Category folder name this series was found under. */
  category: string
  /** Number of chapter files found on disk for this series. */
  chapterCount: number
  /** Provider keys recorded in the sidecar (empty when no provenance is known). */
  providers: string[]
  /** Staging status: 'pending' | 'imported' | 'skipped'. */
  status: string
  /** Whether a Series row with this title/slug already exists in the DB. */
  alreadyInDb: boolean
}

/** One path's failure within a bulk "import all remaining" batch. */
export interface BatchImportFailure {
  path: string
  message: string
}

function mapEntry(dto: FoundSeriesDTO): ScanEntry {
  return {
    path: dto.path,
    title: dto.title,
    category: dto.category,
    chapterCount: dto.chapterCount,
    providers: dto.providers,
    status: dto.status,
    alreadyInDb: dto.alreadyInDb,
  }
}

function mapBatchFailure(dto: BatchImportFailureDTO): BatchImportFailure {
  return { path: dto.path, message: dto.message }
}

/** The live state of the background scan — see the terminal-latch note above. */
export interface ScanState {
  status: 'idle' | 'scanning' | 'done'
  processed: number
  total: number
  /** Non-empty when scan.done carried an error (e.g. a timed-out walk). */
  error: string
}

/** Shape of the scan.progress / scan.done SSE payloads (all fields optional — Go's `omitempty`). */
interface ScanEventPayload {
  processed?: number
  total?: number
  path?: string
  found?: number
  error?: string
}

export function useScanLibrary() {
  const { on } = useProgressStream()

  // ── Scan lifecycle ──────────────────────────────────────────────────────
  const scanState = ref<ScanState>({ status: 'idle', processed: 0, total: 0, error: '' })

  /** Launches the async backend scan; 409 (already running) is not an error. */
  async function startScan(): Promise<void> {
    const res = await apiClient.POST('/api/library/scan')
    if (res.response.status === 409) {
      // A scan is already in flight (single-flight guard) — join it rather
      // than treating the double-click/re-open as a failure.
      scanState.value = { status: 'scanning', processed: 0, total: 0, error: '' }
      return
    }
    if (res.error || !res.data) {
      const message = res.error && 'message' in res.error ? res.error.message : 'Failed to start scan'
      scanState.value = { ...scanState.value, error: message }
      return
    }
    scanState.value = { status: 'scanning', processed: 0, total: 0, error: '' }
  }

  const unsubStart = on('scan.start', () => {
    scanState.value = { status: 'scanning', processed: 0, total: 0, error: '' }
  })

  const unsubProgress = on('scan.progress', (data) => {
    // Terminal latch: ignore any progress frame once the scan has already
    // finished — a scan.progress arriving after scan.done (the backend's
    // leaked-goroutine-on-timeout case) must never revive 'scanning'.
    if (scanState.value.status !== 'scanning') return
    const payload = data as ScanEventPayload
    scanState.value = {
      status: 'scanning',
      processed: payload.processed ?? scanState.value.processed,
      total: payload.total ?? scanState.value.total,
      error: '',
    }
  })

  const unsubDone = on('scan.done', (data) => {
    const payload = data as ScanEventPayload
    scanState.value = {
      status: 'done',
      processed: payload.total ?? payload.found ?? scanState.value.processed,
      total: payload.total ?? scanState.value.total,
      error: payload.error ?? '',
    }
    void load(false)
  })

  onUnmounted(() => {
    unsubStart()
    unsubProgress()
    unsubDone()
  })

  // ── Staged-entries list (paginated) ─────────────────────────────────────
  const entries = ref<ScanEntry[]>([])
  const statusFilter = ref<ScanStatusFilter>(null)
  const offset = ref(0)
  const pending = ref(false)
  // Load failure for the entries list itself (distinct from a per-row
  // mutation error — see rowError/error(path) below). Named entriesError so
  // it doesn't collide with the per-row `error(path)` lookup function
  // exposed on the composable's public surface.
  const entriesError = ref('')
  const hasMore = ref(false)

  async function load(append: boolean): Promise<void> {
    pending.value = true
    entriesError.value = ''
    if (!append) offset.value = 0
    try {
      const res = await apiClient.GET('/api/library/imports', {
        params: {
          query: {
            status: statusFilter.value ?? undefined,
            limit: PAGE,
            offset: offset.value,
          },
        },
      })
      if (res.error || !res.data) {
        throw new Error(res.error && 'message' in res.error ? res.error.message : 'Failed to load staged entries')
      }
      const page = res.data.map(mapEntry)
      entries.value = append ? [...entries.value, ...page] : page
      hasMore.value = page.length === PAGE
    }
    catch (e) {
      entriesError.value = e instanceof Error ? e.message : 'Failed to load staged entries'
    }
    finally {
      pending.value = false
    }
  }

  /** Switches the staging-status filter and reloads from offset 0. */
  function setStatusFilter(status: ScanStatusFilter): void {
    statusFilter.value = status
    void load(false)
  }

  /** Loads the next page and appends it to the current entries. */
  function loadMore(): void {
    offset.value += PAGE
    void load(true)
  }

  // ── Per-row mutation state (busy(path)/error(path)) ─────────────────────
  const busyPaths = ref<Set<string>>(new Set())
  const rowErrors = ref<Record<string, string>>({})

  /**
   * Shared per-row mutate helper (mirrors useCategories.ts's mutate(busyId,
   * fn)): marks `path` busy, runs `fn`, refetches the entries list on
   * success, and records any thrown error against `path` — every row tracks
   * its own busy/error state independently, since several rows can be
   * skipped/imported in quick succession from the same table.
   */
  async function mutate(path: string, fn: () => Promise<void>): Promise<void> {
    busyPaths.value = new Set(busyPaths.value).add(path)
    // Clear any previous error for this path (rebuild rather than `delete` —
    // dynamic-key delete is disallowed by lint and this is O(n) either way).
    rowErrors.value = Object.fromEntries(
      Object.entries(rowErrors.value).filter(([p]) => p !== path),
    )
    try {
      await fn()
      await load(false)
    }
    catch (e) {
      rowErrors.value = { ...rowErrors.value, [path]: e instanceof Error ? e.message : 'Action failed' }
    }
    finally {
      const next = new Set(busyPaths.value)
      next.delete(path)
      busyPaths.value = next
    }
  }

  /** True while `path`'s skip/import mutation is in flight. */
  function busy(path: string): boolean {
    return busyPaths.value.has(path)
  }

  /** The last error message for `path`'s mutation, or '' if none/cleared. */
  function rowError(path: string): string {
    return rowErrors.value[path] ?? ''
  }

  /** Marks a staged entry skipped — leaves it on disk, no re-import prompt. */
  async function skip(path: string): Promise<void> {
    await mutate(path, async () => {
      const res = await apiClient.POST('/api/library/imports/skip', { body: { path } })
      if (res.error) {
        throw new Error('message' in res.error ? res.error.message : 'Failed to skip entry')
      }
    })
  }

  /** Imports a staged entry disk-only (no Suwayomi source attached). */
  async function importDiskOnly(path: string): Promise<void> {
    await mutate(path, async () => {
      const res = await apiClient.POST('/api/library/import', { body: { path } })
      if (res.error || !res.data) {
        throw new Error(res.error && 'message' in res.error ? res.error.message : 'Import failed')
      }
    })
  }

  /**
   * Imports a staged entry and attaches zero or more owner-gathered,
   * best-first Suwayomi sources (Slice P — was a single fixed-importance
   * `match`). An empty `matches` array is disk-only, same as `importDiskOnly`.
   */
  async function importWithMatches(path: string, matches: ProviderRef[]): Promise<void> {
    await mutate(path, async () => {
      const res = await apiClient.POST('/api/library/import', { body: { path, matches } })
      if (res.error || !res.data) {
        throw new Error(res.error && 'message' in res.error ? res.error.message : 'Import failed')
      }
    })
  }

  // ── Per-scanlator breakdown cache (Configure-stage auto-split, Slice P) ──
  // Keyed by `source:mangaId`. `null` = fetch attempted and failed (the panel
  // falls back to a single unsplit row); an absent key = not yet attempted.
  const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
  const breakdownsInFlight = new Set<string>()

  /**
   * Fetches the per-scanlator breakdown for every given candidate IN
   * PARALLEL, skipping any candidate already cached (success or failure) or
   * already in flight. Never throws — a per-candidate failure caches `null`
   * and is otherwise swallowed (non-fatal; the Configure stage renders that
   * source as a single unsplit row). Copied from `useMatchSource.
   * loadBreakdowns` (§2 DRY — identical cache/in-flight-guard/parallel-fetch
   * shape).
   */
  async function loadBreakdowns(candidates: SearchCandidate[]): Promise<void> {
    const toFetch = candidates.filter((c) => {
      const key = breakdownKey(c.source, c.mangaId)
      return !(key in breakdowns.value) && !breakdownsInFlight.has(key)
    })
    if (toFetch.length === 0) return
    for (const c of toFetch) breakdownsInFlight.add(breakdownKey(c.source, c.mangaId))
    await Promise.all(toFetch.map(async (c) => {
      const key = breakdownKey(c.source, c.mangaId)
      try {
        const res = await apiClient.GET('/api/sources/{sourceId}/manga/{mangaId}/breakdown', {
          params: { path: { sourceId: c.source, mangaId: c.mangaId } },
        })
        breakdowns.value = {
          ...breakdowns.value,
          [key]: res.error || !res.data ? null : res.data.scanlators.map(mapScanlatorCoverage),
        }
      }
      catch {
        breakdowns.value = { ...breakdowns.value, [key]: null }
      }
      finally {
        breakdownsInFlight.delete(key)
      }
    }))
  }

  // ── Cross-source match search ────────────────────────────────────────────
  const matching = ref(false)
  const matchError = ref('')
  /** The active match target's cross-source candidate groups (see the
   * stale-response guard on `match()` below — this ref only ever reflects
   * the MOST RECENTLY STARTED request, never an earlier one that happens to
   * resolve later). */
  const matchGroups = ref<SearchGroup[]>([])
  /**
   * Monotonic request-generation counter for `match()`'s stale-response
   * guard (Task-7 review fix). The owner can click Match on series A (a
   * slow cross-source search), go Back, then click Match on series B (a
   * fast one) before A's promise settles — without a guard, A's response
   * would land AFTER B's and clobber `matchGroups`/`matchError` with the
   * WRONG series' candidates, letting the owner attach A's chosen source to
   * B's on-disk path. A plain `path` equality check is not enough (the same
   * path re-matched twice must ALSO discard the earlier response), so each
   * call captures its own generation and only writes shared state back if
   * it is still the most recent one when its `await` resolves.
   */
  let matchGeneration = 0

  /**
   * Searches every Suwayomi source for a staged entry's title. Always
   * returns this call's own mapped groups (even if stale) so a caller that
   * wants the raw result directly still can — but the SHARED `matchGroups`/
   * `matching`/`matchError` refs are only updated when this call is still
   * the latest one in flight (see the generation-counter guard above).
   */
  async function match(path: string): Promise<SearchGroup[]> {
    const generation = ++matchGeneration
    matching.value = true
    matchError.value = ''
    try {
      const res = await apiClient.GET('/api/library/imports/match', { params: { query: { path } } })
      if (res.error || !res.data) {
        throw new Error(res.error && 'message' in res.error ? res.error.message : 'Match search failed')
      }
      const groups = res.data.map(mapGroup)
      if (generation === matchGeneration) matchGroups.value = groups
      return groups
    }
    catch (e) {
      const message = e instanceof Error ? e.message : 'Match search failed'
      if (generation === matchGeneration) matchError.value = message
      return []
    }
    finally {
      if (generation === matchGeneration) matching.value = false
    }
  }

  // ── Bulk "import all remaining as disk-only" ─────────────────────────────
  const batchImporting = ref(false)
  const batchError = ref('')
  const batchResult = ref<{ imported: number, failed: BatchImportFailure[] } | null>(null)

  /** Fixed page size used while draining the pending list (§ below). */
  const DRAIN_PAGE = 200
  /** The batch-import endpoint's validated per-request cap. */
  const BATCH_CAP = 500

  /**
   * Imports EVERY currently-pending staged entry disk-only — a true
   * import-all, not a single-batch-capped one (the owner is migrating
   * 1000+ series; one 500-row batch would silently strand the rest with no
   * signal that more remained).
   *
   * Two phases, run strictly in order:
   *   1. DRAIN — page through `GET .../imports?status=pending` (`DRAIN_PAGE`
   *      rows at a time) collecting every pending path into one array,
   *      until a page comes back shorter than `DRAIN_PAGE` (no more pages).
   *      This phase ONLY reads; it never mutates any entry.
   *   2. CHUNK — slice the fully-drained path array into ≤`BATCH_CAP` chunks
   *      and POST `/api/library/import/batch` once per chunk, summing
   *      `imported` and concatenating `failed` across every chunk so a
   *      library far larger than one batch is still covered in full.
   *
   * Termination is safe by construction: the full pending list is captured
   * BEFORE any batch runs (phase 1 fully precedes phase 2), so a path that
   * fails its import (and therefore stays `pending`) is never re-drained or
   * re-batched — this function makes exactly one drain pass, ever. The
   * drain offset also advances by the FIXED `DRAIN_PAGE` every iteration
   * (never derived from how many rows a page happened to contain), so a
   * short/empty non-terminal page can't leave the offset stuck and loop
   * forever.
   */
  async function importAllDiskOnly(): Promise<void> {
    batchImporting.value = true
    batchError.value = ''
    batchResult.value = null
    try {
      // Phase 1 — drain: collect every pending path, read-only.
      const paths: string[] = []
      let drainOffset = 0
      for (;;) {
        const res = await apiClient.GET('/api/library/imports', {
          params: { query: { status: 'pending', limit: DRAIN_PAGE, offset: drainOffset } },
        })
        if (res.error || !res.data) {
          throw new Error(res.error && 'message' in res.error ? res.error.message : 'Failed to load pending entries')
        }
        paths.push(...res.data.map(d => d.path))
        if (res.data.length < DRAIN_PAGE) break
        drainOffset += DRAIN_PAGE
      }

      if (paths.length === 0) {
        // Defense-in-depth: nothing to drain, but still surface an explicit
        // zero outcome rather than returning silently — a caller (the
        // owner clicking "Import all remaining" a second time after
        // everything is already imported/skipped) must always see a result
        // banner, never a busy-flip-with-no-feedback (§16).
        batchResult.value = { imported: 0, failed: [] }
        return
      }

      // Phase 2 — chunk + import: every drained path is covered, chunk by
      // chunk, accumulating across the whole array.
      let imported = 0
      const failed: BatchImportFailure[] = []
      for (let i = 0; i < paths.length; i += BATCH_CAP) {
        const chunk = paths.slice(i, i + BATCH_CAP)
        const res = await apiClient.POST('/api/library/import/batch', { body: { paths: chunk } })
        if (res.error || !res.data) {
          throw new Error(res.error && 'message' in res.error ? res.error.message : 'Batch import failed')
        }
        imported += res.data.imported
        failed.push(...res.data.failed.map(mapBatchFailure))
      }

      batchResult.value = { imported, failed }
      await load(false)
    }
    catch (e) {
      batchError.value = e instanceof Error ? e.message : 'Batch import failed'
    }
    finally {
      batchImporting.value = false
    }
  }

  void load(false)

  return {
    scanState,
    startScan,
    entries,
    statusFilter,
    setStatusFilter,
    pending,
    entriesError,
    hasMore,
    loadMore,
    busy,
    error: rowError,
    skip,
    importDiskOnly,
    importWithMatches,
    breakdowns,
    loadBreakdowns,
    matching,
    matchError,
    matchGroups,
    match,
    batchImporting,
    batchError,
    batchResult,
    importAllDiskOnly,
    refresh: () => load(false),
  }
}
