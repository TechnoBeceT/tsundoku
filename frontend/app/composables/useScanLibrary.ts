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
 * Per-row mutations (`skip`/`importDiskOnly`/`importWithMatch`) follow the
 * shared `mutate(busyId, fn)` convention from `useCategories.ts`: set busy,
 * run the call, refetch the list on success, clear busy. Unlike
 * `useCategories` (whose UI only ever shows one row's error, e.g. inside a
 * modal), the Scan-Library table can show MANY rows with independent errors
 * at once, so busy/error state is tracked per-path (a `Set`/`Record`) rather
 * than a single shared field — exposed as the `busy(path)`/`error(path)`
 * lookup functions the wizard screen (Task 6/7) will call per row.
 */
import { ref, onUnmounted } from 'vue'
import { useProgressStream } from '~/composables/useProgressStream'
import { mapGroup } from '~/composables/importMappers'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SearchGroup } from '~/components/screens/import.types'

type FoundSeriesDTO = components['schemas']['FoundSeries']
type BatchImportFailureDTO = components['schemas']['BatchImportFailure']

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

/** A ranked Suwayomi source to attach when importing a staged entry. */
export interface ScanMatch {
  /** Suwayomi source ID the chosen candidate came from. */
  source: string
  /** Suwayomi-internal manga identifier within that source. */
  mangaId: number
  /** Provider importance to assign (higher number = higher priority). */
  importance: number
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

  /** Imports a staged entry and attaches an owner-matched Suwayomi source. */
  async function importWithMatch(path: string, match: ScanMatch): Promise<void> {
    await mutate(path, async () => {
      const res = await apiClient.POST('/api/library/import', { body: { path, match } })
      if (res.error || !res.data) {
        throw new Error(res.error && 'message' in res.error ? res.error.message : 'Import failed')
      }
    })
  }

  // ── Cross-source match search ────────────────────────────────────────────
  const matching = ref(false)
  const matchError = ref('')

  /** Searches every Suwayomi source for a staged entry's title. */
  async function match(path: string): Promise<SearchGroup[]> {
    matching.value = true
    matchError.value = ''
    try {
      const res = await apiClient.GET('/api/library/imports/match', { params: { query: { path } } })
      if (res.error || !res.data) {
        throw new Error(res.error && 'message' in res.error ? res.error.message : 'Match search failed')
      }
      return res.data.map(mapGroup)
    }
    catch (e) {
      matchError.value = e instanceof Error ? e.message : 'Match search failed'
      return []
    }
    finally {
      matching.value = false
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

      if (paths.length === 0) return

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
    importWithMatch,
    matching,
    matchError,
    match,
    batchImporting,
    batchError,
    batchResult,
    importAllDiskOnly,
    refresh: () => load(false),
  }
}
