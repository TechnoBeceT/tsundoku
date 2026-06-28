/**
 * useHealth — data layer for the Library Health screen.
 *
 * Fetches GET /api/health, unwraps the { series } envelope, and maps the
 * generated backend DTOs onto the screen's SeriesHealth[] / Provider[] types.
 *
 * pending    — true during the initial load (skeleton state).
 * refreshing — true during a manual re-poll (spinner on the Rescan button).
 *              Distinct from pending so the screen can keep the existing cards
 *              visible while re-fetching instead of blanking them with skeletons.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SeriesHealth } from '~/components/screens/libraryHealth.types'
import type { Provider } from '~/components/screens/seriesDetail.types'

type SeriesHealthDTO = components['schemas']['SeriesHealth']
type ProviderDTO = components['schemas']['Provider']

/**
 * Map one backend Provider DTO onto the screen's Provider.
 *
 * Dropped fields (not on the screen type): title, coverUrl, isMetadataSource.
 * newestChapterAt / lastSyncedAt: optional in the DTO, nullable in the screen
 * type — normalised with ?? null.
 */
function mapProvider(dto: ProviderDTO): Provider {
  return {
    id: dto.id,
    provider: dto.provider,
    scanlator: dto.scanlator,
    language: dto.language,
    importance: dto.importance,
    health: dto.health,
    chaptersBehind: dto.chaptersBehind,
    newestChapterAt: dto.newestChapterAt ?? null,
    lastSyncedAt: dto.lastSyncedAt ?? null,
    lastError: dto.lastError,
  }
}

/**
 * Map one backend SeriesHealth DTO onto the screen's SeriesHealth.
 *
 * The envelope (LibraryHealth.series[]) is unwrapped by the caller; this maps
 * only a single entry. `sources` carries only the unhealthy providers — the
 * screen renders every source it receives.
 */
function mapSeriesHealth(dto: SeriesHealthDTO): SeriesHealth {
  return {
    id: dto.id,
    title: dto.title,
    slug: dto.slug,
    sources: dto.sources.map(mapProvider),
  }
}

export function useHealth() {
  const series = ref<SeriesHealth[]>([])
  const pending = ref(false)
  const refreshing = ref(false)
  const error = ref<string | null>(null)

  /** Shared fetch logic; isRefresh=true toggles refreshing instead of pending. */
  async function load(isRefresh: boolean): Promise<void> {
    if (isRefresh) {
      refreshing.value = true
    }
    else {
      pending.value = true
    }
    error.value = null
    try {
      const res = await apiClient.GET('/api/health')
      if (res.error || !res.data) throw new Error('Failed to load health data')
      series.value = res.data.series.map(mapSeriesHealth)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load health data'
    }
    finally {
      if (isRefresh) {
        refreshing.value = false
      }
      else {
        pending.value = false
      }
    }
  }

  /** Manual re-poll — keeps existing cards visible; toggles refreshing, not pending. */
  function refresh(): void {
    void load(true)
  }

  // Kick off the initial load immediately.
  void load(false)

  return {
    series,
    pending,
    error,
    refreshing,
    refresh,
  }
}
