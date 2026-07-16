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
      { source: 'a', mangaId: 1, url: '', scanlator: '' },
      { source: 'b', mangaId: 2, url: '', scanlator: '' },
    ])
  })

  it('splits a candidate into one row per scanlator when its breakdown has >=2, none pre-selected', async () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: vi.fn() })
    c.enterConfigure([cand('a', 1, 'Comix')])
    breakdowns.value = { 'a:1': [
      { scanlator: 'Reset', count: 90, ranges: '1-90' },
      { scanlator: 'Asura', count: 10, ranges: '91-100' },
    ] }
    await nextTick()
    expect(c.displayRows.value.map(r => r.key)).toEqual(['a:1:Reset', 'a:1:Asura'])
    // Owner-chosen: a split never inherits the pre-split "select all" default —
    // the user must explicitly pick which scanlator(s) to attach.
    expect(c.displayRows.value.every(r => !r.selected)).toBe(true)
    expect(c.selectedCount.value).toBe(0)
    expect(c.orderedProviders.value).toEqual<ProviderRef[]>([])
  })

  it('deselect-on-split: user can then select exactly one scanlator via toggleCand', async () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: vi.fn() })
    c.enterConfigure([cand('a', 1, 'Comix')])
    breakdowns.value = { 'a:1': [
      { scanlator: 'Reset', count: 90, ranges: '1-90' },
      { scanlator: 'Asura', count: 10, ranges: '91-100' },
    ] }
    await nextTick()
    expect(c.selectedCount.value).toBe(0)

    c.toggleCand('a:1:Reset')
    expect(c.selectedCount.value).toBe(1)
    expect(c.orderedProviders.value).toEqual<ProviderRef[]>([
      { source: 'a', mangaId: 1, url: '', scanlator: 'Reset' },
    ])
    const rows = c.displayRows.value
    expect(rows.find(r => r.key === 'a:1:Reset')?.selected).toBe(true)
    expect(rows.find(r => r.key === 'a:1:Asura')?.selected).toBe(false)
  })

  it('a source deselected before its breakdown resolves stays deselected after split', async () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: vi.fn() })
    c.enterConfigure([cand('a', 1, 'Comix'), cand('b', 2)])
    c.toggleCand('a:1') // deselect before the breakdown resolves
    expect(c.selectedCount.value).toBe(1) // only 'b:2' remains selected

    breakdowns.value = { 'a:1': [
      { scanlator: 'Reset', count: 90, ranges: '1-90' },
      { scanlator: 'Asura', count: 10, ranges: '91-100' },
    ] }
    await nextTick()
    expect(c.displayRows.value.filter(r => r.key.startsWith('a:1')).every(r => !r.selected)).toBe(true)
    expect(c.selectedCount.value).toBe(1)
    expect(c.orderedProviders.value).toEqual<ProviderRef[]>([{ source: 'b', mangaId: 2, url: '', scanlator: '' }])
  })

  it('a 0/1-scanlator breakdown does not deselect — no split, selection unchanged', async () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: vi.fn() })
    c.enterConfigure([cand('a', 1, 'Comix')])
    expect(c.selectedCount.value).toBe(1)

    breakdowns.value = { 'a:1': [{ scanlator: 'Comix', count: 100, ranges: '1-100' }] }
    await nextTick()
    expect(c.selectedCount.value).toBe(1)
    expect(c.displayRows.value[0]?.selected).toBe(true)
  })

  it('breakdownsResolving: true right after enterConfigure, false once every candidate key resolves', async () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: vi.fn() })
    c.enterConfigure([cand('a', 1), cand('b', 2)])
    expect(c.breakdownsResolving.value).toBe(true)

    breakdowns.value = { ...breakdowns.value, 'a:1': [{ scanlator: 'a', count: 5, ranges: '1-5' }] }
    await nextTick()
    expect(c.breakdownsResolving.value).toBe(true) // 'b:2' still unresolved

    // A resolved-failed (null) entry still counts as resolved — it does not block.
    breakdowns.value = { ...breakdowns.value, 'b:2': null }
    await nextTick()
    expect(c.breakdownsResolving.value).toBe(false)
  })

  it('collapses the untagged (source-name) bucket to scanlator ""', () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: vi.fn() })
    c.enterConfigure([cand('a', 1, 'Comix')])
    breakdowns.value = { 'a:1': [{ scanlator: 'Comix', count: 100, ranges: '1-100' }] }
    expect(c.orderedProviders.value).toEqual<ProviderRef[]>([{ source: 'a', mangaId: 1, url: '', scanlator: '' }])
  })

  it('moveCand reorders and orderedProviders follows', () => {
    const breakdowns = ref<Record<string, ScanlatorCoverage[] | null>>({})
    const c = useSourceConfigure({ breakdowns, onLoadBreakdowns: vi.fn() })
    c.enterConfigure([cand('a', 1), cand('b', 2)])
    c.moveCand('b:2', -1)
    expect(c.orderedProviders.value[0]).toEqual({ source: 'b', mangaId: 2, url: '', scanlator: '' })
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
