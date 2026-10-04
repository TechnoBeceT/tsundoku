import { onUnmounted, ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import { candKey } from '~/components/screens/import.types'
import type { CoverageSnapshotView, ScanlatorCoverage, SearchCandidate } from '~/components/screens/import.types'
import { mapCoverageSnapshot, mapScanlatorCoverage } from './importMappers'
import { useProgressStream } from './useProgressStream'

interface AddressRef {
  source: string
  mangaId: number
  url: string
  addressMode?: SearchCandidate['addressMode']
  webUrl?: string
}

function addressOf(candidate: SearchCandidate): AddressRef {
  return {
    source: candidate.source,
    mangaId: candidate.mangaId,
    url: candidate.url,
    addressMode: candidate.addressMode,
    webUrl: candidate.realUrl,
  }
}

/** Per-consumer coverage snapshots keyed by exact source/address. Settled entries
 * are reused until explicit refresh or the matching background completion event. */
export function useSourceCoverage() {
  const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
  const breakdownSnapshots = ref<Record<string, CoverageSnapshotView>>({})
  const inFlight = new Set<string>()
  const addresses = new Map<string, AddressRef>()

  function fail(key: string, error: string): void {
    breakdowns.value = { ...breakdowns.value, [key]: null }
    breakdownSnapshots.value = {
      ...breakdownSnapshots.value,
      [key]: { status: 'failed', computedAt: '', error },
    }
  }

  // Initial loads, explicit refresh and SSE refetch all take the same guard.
  async function fetchBreakdown(address: AddressRef, refresh = false): Promise<void> {
    const key = candKey(address)
    if (inFlight.has(key)) return
    inFlight.add(key)
    addresses.set(key, address)
    try {
      const res = await apiClient.GET('/api/sources/{sourceId}/manga/{mangaId}/breakdown', {
        params: {
          path: { sourceId: address.source, mangaId: address.mangaId },
          query: { url: address.url, addressMode: address.addressMode, webUrl: address.webUrl, refresh: refresh ? true : undefined },
        },
      })
      if (res.error || !res.data) {
        fail(key, res.error ? res.error.message : 'Failed to load breakdown')
        return
      }
      // Pending/failed computation snapshots carry empty scanlators; null means
      // a request-level failure and retains the existing unsplit fallback.
      breakdowns.value = { ...breakdowns.value, [key]: res.data.scanlators.map(mapScanlatorCoverage) }
      breakdownSnapshots.value = { ...breakdownSnapshots.value, [key]: mapCoverageSnapshot(res.data) }
    }
    catch {
      fail(key, 'Failed to load breakdown')
    }
    finally {
      inFlight.delete(key)
    }
  }

  async function loadBreakdowns(candidates: SearchCandidate[]): Promise<void> {
    await Promise.all(candidates.map(async candidate => {
      if (candKey(candidate) in breakdowns.value) return
      await fetchBreakdown(addressOf(candidate))
    }))
  }

  /** Explicit refresh bypasses settled cache entries, but never a live request. */
  async function refreshBreakdown(candidate: SearchCandidate): Promise<void> {
    await fetchBreakdown(addressOf(candidate), true)
  }

  const { on } = useProgressStream()
  const unsubscribe = on('imports.coverage.done', data => {
    const payload = data as { sourceId?: string, mangaUrl?: string }
    if (!payload.sourceId || !payload.mangaUrl) return
    const key = candKey({ source: payload.sourceId, url: payload.mangaUrl })
    const address = addresses.get(key)
    if (address) void fetchBreakdown(address)
  })
  onUnmounted(unsubscribe)

  return { breakdowns, breakdownSnapshots, loadBreakdowns, refreshBreakdown }
}
