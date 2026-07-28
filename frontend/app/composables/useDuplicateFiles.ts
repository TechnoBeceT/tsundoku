/**
 * useDuplicateFiles — data layer for the Cleanup console's Duplicates tab.
 *
 * Owns ONE thing: the list — GET /api/library/duplicate-files, unwrapped and
 * mapped to SeriesDuplicateFiles[] plus the library totals. There is deliberately
 * no removal here: the backend exposes no library-wide execute path, so the tab
 * links each row to its series where the existing owner-triggered "Remove
 * duplicate files" action lives.
 *
 * By default the initial load fires on creation (`immediate: true`). Pass
 * `{ immediate: false }` to defer it — the Cleanup page does this so the scan only
 * runs when the Duplicates tab is first shown (LAZY tab data), then triggers the
 * load itself via `refetch()`. Deferring matters more here than on a typical tab:
 * the endpoint reads every series folder on disk, so an eager load would make
 * opening ANY cleanup tab pay for all three.
 *
 * State refs mirror useSourceless: `pending` gates the initial skeleton;
 * `refreshing` gates the manual re-poll (keeps cards visible).
 *
 * §16: a load failure is surfaced in `error`, never swallowed — the page renders
 * it as a banner rather than showing an empty list that reads as "all clean".
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SeriesDuplicateFiles } from '~/components/screens/duplicates.types'

type SeriesDuplicateFilesDTO = components['schemas']['SeriesDuplicateFilesRow']

/** Map one backend SeriesDuplicateFilesRow DTO onto the screen's row type. */
function mapRow(dto: SeriesDuplicateFilesDTO): SeriesDuplicateFiles {
  return {
    seriesId: dto.seriesId,
    title: dto.title,
    displayName: dto.displayName,
    category: dto.category,
    coverUrl: dto.coverUrl,
    fileCount: dto.fileCount,
    reclaimableBytes: dto.reclaimableBytes,
  }
}

export function useDuplicateFiles(options: { immediate?: boolean } = {}) {
  const { immediate = true } = options

  const series = ref<SeriesDuplicateFiles[]>([])
  const totalFiles = ref(0)
  const totalBytes = ref(0)
  const pending = ref(false)
  const refreshing = ref(false)
  const error = ref<string | null>(null)

  /** Shared list fetch; isRefresh=true toggles refreshing instead of pending. */
  async function load(isRefresh: boolean): Promise<void> {
    if (isRefresh) refreshing.value = true
    else pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/library/duplicate-files')
      if (res.error || !res.data) throw new Error('Failed to load duplicate files')
      series.value = res.data.series.map(mapRow)
      totalFiles.value = res.data.totalFiles
      totalBytes.value = res.data.totalBytes
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load duplicate files'
    }
    finally {
      if (isRefresh) refreshing.value = false
      else pending.value = false
    }
  }

  /** Perform the (possibly deferred) initial load — the lazy tab's entry point. */
  function refetch(): Promise<void> {
    return load(false)
  }

  /** Manual re-poll — keeps existing cards visible; toggles refreshing, not pending. */
  function refresh(): Promise<void> {
    return load(true)
  }

  if (immediate) void load(false)

  return {
    series,
    totalFiles,
    totalBytes,
    pending,
    refreshing,
    error,
    refetch,
    refresh,
  }
}
