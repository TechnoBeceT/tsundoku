/**
 * Series-detail PAGE — the resume FAB (Komikku-style "continue reading" button).
 *
 * `useReadingProgress.resumeTarget` already computes the resume point
 * (furthest-along downloaded chapter, else the first); this pins that the
 * PAGE actually surfaces it: "Start" when nothing has been read, "Continue"
 * once there's progress, and — the point of the whole feature — hidden
 * entirely when the series has no downloaded chapters (nothing to resume).
 *
 * Mirrors `id.test.ts`'s idiom: the page is mounted for real (mountSuspended)
 * so the wiring under test is the wiring that ships; only the API client and
 * the route are faked.
 */
import { describe, it, expect, vi } from 'vitest'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import { flushPromises } from '@vue/test-utils'
// See id.test.ts: the series-detail page lives at `[id]/index.vue`, not `[id].vue`.
import Page from './[id]/index.vue'

mockNuxtImport('useRoute', () => () => ({ params: { id: 'series-1' } }))

interface ChapterFixture {
  id: string
  chapterKey: string
  number: number | null
  name: string
  state: string
  filename: string
  pageCount: number | null
  read: boolean
  lastReadPage: number
  readAt: string | null
}

function chapter(over: Partial<ChapterFixture> = {}): ChapterFixture {
  return {
    id: 'ch-1',
    chapterKey: 'ch-1',
    number: 1,
    name: 'Chapter One',
    state: 'downloaded',
    filename: '[src][en] Series 0001.cbz',
    pageCount: 20,
    read: false,
    lastReadPage: 0,
    readAt: null,
    ...over,
  }
}

// Mutated per-test before mounting; read by the mocked GET below.
let chapters: ChapterFixture[] = []

function detail() {
  return {
    id: 'series-1',
    displayName: 'Solo Leveling',
    slug: 'solo-leveling',
    category: 'Manhwa',
    coverUrl: '',
    monitored: true,
    completed: false,
    chapterCounts: {
      total: chapters.length,
      downloaded: chapters.filter((c) => c.state === 'downloaded').length,
      wanted: 0,
      failed: 0,
    },
    chapters,
    providers: [],
  }
}

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/series/{id}') {
        return Promise.resolve({ data: detail(), error: null, response: new Response() })
      }
      return Promise.resolve({ data: [], error: null, response: new Response() })
    }),
    POST: vi.fn(() => Promise.resolve({ data: null, error: null, response: new Response() })),
    PATCH: vi.fn(() => Promise.resolve({ data: null, error: null, response: new Response() })),
    DELETE: vi.fn(() => Promise.resolve({ data: null, error: null, response: new Response() })),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('series detail page — resume FAB', () => {
  it('is not rendered when the series has no downloaded chapters', async () => {
    chapters = [chapter({ state: 'wanted', filename: '', pageCount: null })]
    const wrapper = await mountSuspended(Page)
    await flushPromises()

    expect(wrapper.text()).not.toContain('Start')
    expect(wrapper.text()).not.toContain('Continue')
  })

  it('labels "Start" when no downloaded chapter has been opened', async () => {
    chapters = [chapter({ read: false, lastReadPage: 0 })]
    const wrapper = await mountSuspended(Page)
    await flushPromises()

    expect(wrapper.text()).toContain('Start')
    expect(wrapper.text()).not.toContain('Continue')
  })

  it('labels "Continue" once a downloaded chapter shows progress', async () => {
    chapters = [chapter({ read: false, lastReadPage: 5 })]
    const wrapper = await mountSuspended(Page)
    await flushPromises()

    expect(wrapper.text()).toContain('Continue')
    expect(wrapper.text()).not.toContain('Start')
  })
})
