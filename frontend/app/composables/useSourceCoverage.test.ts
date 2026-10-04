import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount, type VueWrapper } from '@vue/test-utils'
import { useSourceCoverage } from './useSourceCoverage'
import type { SearchCandidate } from '../components/screens/import.types'

const { get, listeners } = vi.hoisted(() => ({
  get: vi.fn().mockResolvedValue({ data: null, response: { status: 200 } }),
  listeners: new Set<(payload: unknown) => void>(),
}))
vi.mock('~/utils/api/client', () => ({ apiClient: { GET: get }, setUnauthorizedHandler: vi.fn() }))
vi.mock('~/composables/useProgressStream', () => ({ useProgressStream: () => ({
  on(event: string, callback: (payload: unknown) => void) {
    expect(event).toBe('imports.coverage.done')
    listeners.add(callback)
    return () => listeners.delete(callback)
  },
}) }))

const a = { source: '11', mangaId: 0, url: '/a', title: 'A', sourceName: 'Source', lang: 'en', thumbnailUrl: '', addressMode: 'url_search', realUrl: 'https://source.example/a' } satisfies SearchCandidate
const b = { ...a, url: '/b', realUrl: 'https://source.example/b' }
const keyA = '["11","/a"]'
const keyB = '["11","/b"]'
function snapshot(status: 'ready' | 'pending' | 'failed', label = 'A') {
  return { data: { status, computedAt: status === 'ready' ? '2026-10-04T00:00:00Z' : '', error: status === 'failed' ? 'walk failed' : '', scanlators: status === 'ready' ? [{ scanlator: label, count: 2, ranges: '1-2' }] : [] }, error: null, response: new Response() }
}
const wrappers: VueWrapper[] = []
function setup() {
  let api!: ReturnType<typeof useSourceCoverage>
  wrappers.push(mount(defineComponent({ setup() { api = useSourceCoverage(); return () => null } })))
  return api
}
function emit(payload: unknown) { for (const callback of listeners) callback(payload) }

beforeEach(() => { get.mockReset(); listeners.clear() })
afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount() })

describe('source coverage lifecycle', () => {
  it('preserves complete address witnesses and serves a settled cache without refetching', async () => {
    get.mockResolvedValue(snapshot('ready'))
    const api = setup()
    await api.loadBreakdowns([a])
    await api.loadBreakdowns([a])
    expect(get).toHaveBeenCalledOnce()
    expect(get).toHaveBeenCalledWith('/api/sources/{sourceId}/manga/{mangaId}/breakdown', {
      params: { path: { sourceId: '11', mangaId: 0 }, query: { url: '/a', addressMode: 'url_search', webUrl: 'https://source.example/a', refresh: undefined } },
    })
    expect(api.breakdowns.value[keyA]).toEqual([{ scanlator: 'A', count: 2, ranges: '1-2' }])
    expect(api.breakdownSnapshots.value[keyA]).toEqual({ status: 'ready', computedAt: '2026-10-04T00:00:00Z', error: '' })
  })
  it('deduplicates overlapping requests for an address without blocking another address', async () => {
    let resolveA!: (value: ReturnType<typeof snapshot>) => void
    get.mockImplementation((_path: string, opts: { params: { query: { url: string } } }) => opts.params.query.url === '/a' ? new Promise(resolve => { resolveA = resolve }) : Promise.resolve(snapshot('ready', 'B')))
    const api = setup()
    const initial = api.loadBreakdowns([a])
    await api.loadBreakdowns([a, b])
    await api.refreshBreakdown(a)
    emit({ sourceId: '11', mangaUrl: '/a' })
    expect(get).toHaveBeenCalledTimes(2)
    expect(api.breakdowns.value[keyB]?.[0]?.scanlator).toBe('B')
    resolveA(snapshot('ready'))
    await initial
    expect(api.breakdowns.value[keyA]?.[0]?.scanlator).toBe('A')
    expect(api.breakdowns.value[keyB]?.[0]?.scanlator).toBe('B')
  })
  it('retains pending and failed snapshots and overwrites only the completed address on SSE', async () => {
    get.mockResolvedValue(snapshot('pending'))
    const api = setup()
    await api.loadBreakdowns([a, b])
    await api.loadBreakdowns([a, b])
    expect(get).toHaveBeenCalledTimes(2)
    expect(api.breakdowns.value[keyA]).toEqual([])
    expect(api.breakdownSnapshots.value[keyA]?.status).toBe('pending')
    get.mockResolvedValue(snapshot('failed'))
    emit({ sourceId: '11', mangaUrl: '/a' })
    await vi.waitFor(() => expect(api.breakdownSnapshots.value[keyA]?.status).toBe('failed'))
    expect(api.breakdownSnapshots.value[keyA]?.error).toBe('walk failed')
    expect(api.breakdowns.value[keyA]).toEqual([])
    expect(api.breakdownSnapshots.value[keyB]?.status).toBe('pending')
    const opts = get.mock.calls.at(-1)?.[1] as { params: { query: { refresh?: boolean, url: string } } }
    expect(opts.params.query).toMatchObject({ url: '/a', refresh: undefined })
  })
  it('caches request failures as null and permits an explicit witnessed refresh', async () => {
    get.mockResolvedValue({ data: null, error: { message: 'unreachable' }, response: new Response(null, { status: 502 }) })
    const api = setup()
    await api.loadBreakdowns([a])
    await api.loadBreakdowns([a])
    expect(get).toHaveBeenCalledOnce()
    expect(api.breakdowns.value[keyA]).toBeNull()
    expect(api.breakdownSnapshots.value[keyA]).toEqual({ status: 'failed', computedAt: '', error: 'unreachable' })
    get.mockResolvedValue(snapshot('ready'))
    await api.refreshBreakdown(a)
    const opts = get.mock.calls.at(-1)?.[1] as { params: { query: { refresh?: boolean, webUrl: string, addressMode: string } } }
    expect(opts.params.query).toMatchObject({ refresh: true, webUrl: 'https://source.example/a', addressMode: 'url_search' })
    expect(api.breakdowns.value[keyA]?.[0]?.scanlator).toBe('A')
  })
  it('contains thrown fetch failures and releases the address guard for refresh', async () => {
    get.mockRejectedValue(new Error('network failed'))
    const api = setup()
    await expect(api.loadBreakdowns([a])).resolves.toBeUndefined()
    expect(api.breakdowns.value[keyA]).toBeNull()
    expect(api.breakdownSnapshots.value[keyA]?.error).toBe('Failed to load breakdown')
    get.mockResolvedValue(snapshot('ready'))
    await api.refreshBreakdown(a)
    expect(api.breakdowns.value[keyA]?.[0]?.scanlator).toBe('A')
  })
  it('keeps cache ownership per consumer and unsubscribes on unmount', async () => {
    get.mockResolvedValueOnce(snapshot('ready', 'First')).mockResolvedValueOnce(snapshot('ready', 'Second'))
    const first = setup()
    const second = setup()
    await first.loadBreakdowns([a])
    await second.loadBreakdowns([a])
    expect(get).toHaveBeenCalledTimes(2)
    expect(first.breakdowns.value[keyA]?.[0]?.scanlator).toBe('First')
    expect(second.breakdowns.value[keyA]?.[0]?.scanlator).toBe('Second')
    wrappers.pop()!.unmount()
    expect(listeners.size).toBe(1)
    wrappers.pop()!.unmount()
    expect(listeners.size).toBe(0)
    emit({ sourceId: '11', mangaUrl: '/a' })
    expect(get).toHaveBeenCalledTimes(2)
  })
})
