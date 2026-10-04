import { describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { useSourceSearch } from './useSourceSearch'
const get = vi.hoisted(() => vi.fn())
vi.mock('~/utils/api/client', () => ({ apiClient: { GET: get }, setUnauthorizedHandler: vi.fn() }))
function stream(text: string) {
  const bytes = new TextEncoder().encode(text)
  return new ReadableStream<Uint8Array>({ start(c) { for (const b of bytes) c.enqueue(new Uint8Array([b])); c.close() } })
}
function setup() {
  let search!: ReturnType<typeof useSourceSearch>
  const wrapper = mount(defineComponent({ setup() { search = useSourceSearch(); return () => null } }))
  return { search, wrapper }
}
describe('progressive source search', () => {
  it('decodes split UTF8 and frames', async () => {
    get.mockResolvedValue({ data: stream('event: search\ndata: {"groups":[{"title":"漫画","candidates":[]}],"pendingSources":[],"done":true}\n\n'), response: new Response() })
    const { search, wrapper } = setup()
    await search.search({ q: '漫画', sources: [] })
    expect(search.groups.value[0]?.title).toBe('漫画')
    expect(search.error.value).toBeNull()
    expect(search.searching.value).toBe(false)
    wrapper.unmount()
  })
  it('reports streams ending before done', async () => {
    get.mockResolvedValue({ data: stream('event: search\ndata: {"groups":[],"pendingSources":[],"done":false}\n\n'), response: new Response() })
    const { search, wrapper } = setup()
    await search.search({ q: 'first', sources: [] })
    expect(search.error.value).toMatch(/ended/)
    wrapper.unmount()
  })
  it('aborts superseded requests and rejects their late writes', async () => {
    let resolve!: (value: unknown) => void
    get.mockImplementationOnce(() => new Promise(r => { resolve = r }))
    const { search, wrapper } = setup()
    const first = search.search({ q: 'old', sources: [] })
    const options = get.mock.calls.at(-1)?.[1] as { signal: AbortSignal }
    const signal = options.signal
    get.mockResolvedValue({ data: stream('event: search\ndata: {"groups":[{"title":"new","candidates":[]}],"pendingSources":[],"done":true}\n\n'), response: new Response() })
    await search.search({ q: 'new', sources: [] })
    expect(signal.aborted).toBe(true)
    resolve({ data: stream('event: search\ndata: {"groups":[{"title":"old","candidates":[]}],"pendingSources":[],"done":true}\n\n'), response: new Response() })
    await first
    expect(search.groups.value[0]?.title).toBe('new')
    wrapper.unmount()
  })
  it('keeps ready results while pending and on a later terminal error', async () => {
    let writer!: ReadableStreamDefaultController<Uint8Array>
    const body = new ReadableStream<Uint8Array>({ start(c) { writer = c } })
    get.mockResolvedValue({ data: body, response: new Response() })
    const { search, wrapper } = setup()
    const request = search.search({ q: 'title', sources: [] })
    writer.enqueue(new TextEncoder().encode('event: search\ndata: {"groups":[{"title":"ready","candidates":[]}],"pendingSources":[{"id":"2","name":"Slow","lang":"en"}],"done":false}\n\n'))
    await vi.waitFor(() => expect(search.groups.value[0]?.title).toBe('ready'))
    expect(search.searching.value).toBe(true)
    expect(search.pendingSources.value[0]?.name).toBe('Slow')
    writer.enqueue(new TextEncoder().encode('event: error\ndata: {"message":"source disconnected"}\n\n'))
    await request
    expect(search.groups.value[0]?.title).toBe('ready')
    expect(search.error.value).toBe('source disconnected')
    wrapper.unmount()
  })
  it('rejects malformed frames and cancels an active request on unmount', async () => {
    get.mockResolvedValue({ data: stream('event: search\ndata: invalid\n\n'), response: new Response() })
    const { search, wrapper } = setup()
    await search.search({ q: 'title', sources: [] })
    expect(search.error.value).toBeTruthy()
    let resolve!: (value: unknown) => void
    get.mockImplementationOnce(() => new Promise(r => { resolve = r }))
    const request = search.search({ q: 'late', sources: [] })
    const options = get.mock.calls.at(-1)?.[1] as { signal: AbortSignal }
    wrapper.unmount()
    expect(options.signal.aborted).toBe(true)
    resolve({ data: stream('event: search\ndata: {"groups":[{"title":"late","candidates":[]}],"pendingSources":[],"done":true}\n\n'), response: new Response() })
    await request
    expect(search.groups.value).toEqual([])
  })

})
