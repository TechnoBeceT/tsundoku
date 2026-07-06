import { ref, nextTick } from 'vue'
import { describe, it, expect, vi } from 'vitest'
import { useSourceConfigure, type ProviderRef } from './useSourceConfigure'
import type { ScanlatorCoverage, SearchCandidate, SearchGroup } from '../components/screens/import.types'

const cand = (source: string, mangaId: number, sourceName = source): SearchCandidate =>
  ({ source, mangaId, sourceName, title: 'T', url: '', coverUrl: '', lang: 'en' } as unknown as SearchCandidate)

describe('useSourceConfigure', () => {
  it('seeds all candidates selected in list order and requests breakdowns', () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const onLoad = vi.fn()
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: onLoad })
    c.enterConfigure([cand('a', 1), cand('b', 2)])
    expect(c.selectedCount.value).toBe(2)
    expect(onLoad).toHaveBeenCalledOnce()
    expect(c.orderedProviders.value).toEqual<ProviderRef[]>([
      { source: 'a', mangaId: 1, scanlator: '' },
      { source: 'b', mangaId: 2, scanlator: '' },
    ])
  })

  it('splits a candidate into one row per scanlator when its breakdown has >=2', async () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: vi.fn() })
    c.enterConfigure([cand('a', 1, 'Comix')])
    breakdowns.value = { 'a:1': [
      { scanlator: 'Reset', count: 90, ranges: '1-90' },
      { scanlator: 'Asura', count: 10, ranges: '91-100' },
    ] }
    await nextTick()
    expect(c.displayRows.value.map(r => r.key)).toEqual(['a:1:Reset', 'a:1:Asura'])
    expect(c.orderedProviders.value).toEqual<ProviderRef[]>([
      { source: 'a', mangaId: 1, scanlator: 'Reset' },
      { source: 'a', mangaId: 1, scanlator: 'Asura' },
    ])
  })

  it('collapses the untagged (source-name) bucket to scanlator ""', () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: vi.fn() })
    c.enterConfigure([cand('a', 1, 'Comix')])
    breakdowns.value = { 'a:1': [{ scanlator: 'Comix', count: 100, ranges: '1-100' }] }
    expect(c.orderedProviders.value).toEqual<ProviderRef[]>([{ source: 'a', mangaId: 1, scanlator: '' }])
  })

  it('moveCand reorders and orderedProviders follows', () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: vi.fn() })
    c.enterConfigure([cand('a', 1), cand('b', 2)])
    c.moveCand('b:2', -1)
    expect(c.orderedProviders.value[0]).toEqual({ source: 'b', mangaId: 2, scanlator: '' })
  })

  it('tray: addGroup accumulates, isGroupAdded reflects, configureTray seeds', () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: vi.fn() })
    const g: SearchGroup = { title: 'S', candidates: [cand('a', 1), cand('b', 2)] }
    c.addGroup(g)
    expect(c.trayActive.value).toBe(true)
    expect(c.isGroupAdded(g)).toBe(true)
    c.configureTray()
    expect(c.selectedCount.value).toBe(2)
  })
})
