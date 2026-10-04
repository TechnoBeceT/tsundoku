import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { useImport } from './useImport'
import { useMatchSource } from './useMatchSource'
import { useScanLibrary } from './useScanLibrary'
import { useSourceConfigure } from './useSourceConfigure'
import type { SearchCandidate } from '../components/screens/import.types'

const { get, listeners } = vi.hoisted(() => ({ get: vi.fn().mockResolvedValue({ data: null, response: { status: 200 } }), listeners: new Map<string, (data: unknown) => void>() }))
vi.mock('~/utils/api/client', () => ({ apiClient: { GET: get }, setUnauthorizedHandler: vi.fn() }))
vi.mock('~/composables/useProgressStream', () => ({ useProgressStream: () => ({ on(event: string, callback: (data: unknown) => void) { listeners.set(event, callback); return () => listeners.delete(event) } }) }))

const a = { source: '11', mangaId: 0, url: '/a', title: 'A', sourceName: 'Source', lang: 'en', thumbnailUrl: '', addressMode: 'direct', realUrl: '' } satisfies SearchCandidate
const b = { ...a, url: '/b', title: 'B' }
const keyA = '["11","/a"]'
const keyB = '["11","/b"]'
function response(name: string, status = 'ready') {
  return { data: { status, computedAt: '', scanlators: status === 'pending' ? [] : [{ scanlator: name, count: 1, ranges: '1' }, { scanlator: `${name} extra`, count: 1, ranges: '2' }] }, error: null, response: new Response() }
}
const factories = [
  ['Import', useImport],
  ['Add source', () => useMatchSource('series')],
  ['Scan library', useScanLibrary],
] as const

beforeEach(() => { get.mockReset(); listeners.clear() })

describe.each(factories)('%s coverage identity', (_name, factory) => {
  function setup() {
    let api!: ReturnType<typeof factory>
    const wrapper = mount(defineComponent({ setup() { api = factory(); return () => null } }))
    return { api, wrapper }
  }
  it('fetches a second zero-ID address after the first is cached; config never inherits its scanlators', async () => {
    get.mockImplementation((path: string, opts?: { params?: { query?: { url?: string } } }) => Promise.resolve(path.endsWith('/breakdown') ? response(opts?.params?.query?.url === '/a' ? 'A only' : 'B only') : { data: [], response: new Response() }))
    const { api, wrapper } = setup()
    await api.loadBreakdowns([a])
    const cfg = useSourceConfigure({ breakdowns: api.breakdowns, snapshots: api.breakdownSnapshots, onLoadBreakdowns: vi.fn() })
    cfg.enterConfigure([a, b])
    expect(cfg.displayRows.value.filter(row => row.candidate.url === '/b').map(row => row.scanlator)).toEqual([''])
    expect(cfg.breakdownsResolving.value).toBe(true)
    expect(cfg.displayRows.value.find(row => row.candidate.url === '/b')?.coverageStatus).toBeUndefined()
    await api.loadBreakdowns([b])
    expect(get.mock.calls.filter(call => (call[0] as string).endsWith('/breakdown'))).toHaveLength(2)
    expect(api.breakdowns.value[keyA]?.[0]?.scanlator).toBe('A only')
    expect(api.breakdowns.value[keyB]?.[0]?.scanlator).toBe('B only')
    expect(cfg.displayRows.value.filter(row => row.candidate.url === '/b').map(row => row.scanlator)).toEqual(['B only', 'B only extra'])
    wrapper.unmount()
  })
  it('retains distinct concurrent responses when the first address finishes last', async () => {
    let resolveA!: (value: ReturnType<typeof response>) => void
    get.mockImplementation((path: string, opts?: { params?: { query?: { url?: string } } }) => {
      if (!path.endsWith('/breakdown')) return Promise.resolve({ data: [], response: new Response() })
      if (opts?.params?.query?.url === '/a') return new Promise(resolve => { resolveA = resolve })
      return Promise.resolve(response('B only'))
    })
    const { api, wrapper } = setup()
    const first = api.loadBreakdowns([a])
    await api.loadBreakdowns([b])
    expect(api.breakdowns.value[keyB]?.[0]?.scanlator).toBe('B only')
    resolveA(response('A only'))
    await first
    expect(api.breakdowns.value[keyA]?.[0]?.scanlator).toBe('A only')
    expect(api.breakdowns.value[keyB]?.[0]?.scanlator).toBe('B only')
    wrapper.unmount()
  })
  it('refreshes only the exact address named by a coverage completion event', async () => {
    let completed = false
    get.mockImplementation((path: string) => Promise.resolve(path.endsWith('/breakdown') ? response('A only', completed ? 'ready' : 'pending') : { data: [], response: new Response() }))
    const { api, wrapper } = setup()
    await api.loadBreakdowns([a, b])
    get.mockClear()
    completed = true
    listeners.get('imports.coverage.done')?.({ sourceId: '11', mangaUrl: '/a', status: 'ready' })
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(1))
    const options = get.mock.calls[0]?.[1] as { params: { query: { url: string } } }
    expect(options.params.query.url).toBe('/a')
    await vi.waitFor(() => expect(api.breakdownSnapshots.value[keyA]?.status).toBe('ready'))
    expect(api.breakdownSnapshots.value[keyB]?.status).toBe('pending')
    expect(api.breakdowns.value[keyA]?.[0]?.scanlator).toBe('A only')
    expect(api.breakdowns.value[keyB]).toEqual([])
    wrapper.unmount()
  })
})
