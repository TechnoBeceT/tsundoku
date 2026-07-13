/**
 * Series-detail PAGE — the remove-source confirm dialog lifecycle.
 *
 * The bug this pins (owner-reported): confirming a removal removed the source
 * but LEFT THE DIALOG OPEN, its heading degrading to `Remove “”?` once the
 * provider was gone from the list. The screen owned the dialog state yet only
 * EMITTED the removal (fire-and-forget), so it could never learn the outcome.
 * The dialog now lives on the page, which awaits `useSeriesDetail.removeSource`
 * and closes it ONLY on success (§16: a failure stays open, error visible).
 *
 * Non-vacuous: make `useSeriesDetail.mutate` return void again (drop the
 * true/false outcome) and "closes the dialog once the removal succeeds" fails —
 * `ok` is undefined, so the dialog never closes. That is exactly the shipped bug.
 *
 * The page is mounted for real (mountSuspended) so the wiring under test is the
 * wiring that ships; only the API client and the route are faked. The base
 * `Dialog` is stubbed (its reka-ui portal does not render in happy-dom — same
 * approach as MatchDiskProviderDialog.test.ts); the stub keeps the two things
 * these assertions read: it renders only while `open`, and it renders the title.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import { flushPromises } from '@vue/test-utils'
// On this branch the series-detail page lives at `[id]/index.vue`, not `[id].vue`:
// the reader added `[id]/read/[chapterId].vue`, and a page file sitting beside a
// same-named directory makes every route under it an unrenderable NESTED child
// (see app/pages/pagesStructure.test.ts). Keep this import pointing at index.vue.
import Page from './[id]/index.vue'

let removeOk = true
// Flipped by a successful DELETE: the refetch then returns the series WITHOUT the
// removed source, exactly as the backend does. This is what USED to degrade the
// still-open dialog's heading to `Remove “”?` (the name no longer resolved).
let removed = false

const detail = {
  id: 'series-1',
  displayName: 'Solo Leveling',
  slug: 'solo-leveling',
  category: 'Manhwa',
  coverUrl: '',
  monitored: true,
  completed: false,
  // Native-metadata-engine rich fields (Slice D) — required on the real DTO.
  status: '',
  genres: [],
  tags: [],
  altTitles: [],
  authors: [],
  year: 0,
  links: [],
  metadataSource: null,
  coverSource: null,
  chapterCounts: { total: 2, downloaded: 2, wanted: 0, failed: 0 },
  chapters: [],
  providers: [
    {
      id: 'prov-1',
      provider: 'asurascans',
      providerName: 'Asura Scans',
      linked: true,
      mangaId: 42,
      chapterCount: 2,
      feedCount: 2,
      feedRanges: '1-2',
      hasFeed: true,
      scanlator: '',
      language: 'en',
      importance: 2,
      health: 'ok',
      chaptersBehind: 0,
      lastError: '',
      isMetadataSource: true,
    },
  ],
}

mockNuxtImport('useRoute', () => () => ({ params: { id: 'series-1' } }))

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/series/{id}') {
        const data = removed ? { ...detail, providers: [] } : detail
        return Promise.resolve({ data, error: null, response: new Response() })
      }
      return Promise.resolve({ data: [], error: null, response: new Response() })
    }),
    POST: vi.fn(() => Promise.resolve({ data: null, error: null, response: new Response() })),
    PATCH: vi.fn(() => Promise.resolve({ data: null, error: null, response: new Response() })),
    DELETE: vi.fn().mockImplementation(() => {
      if (!removeOk) {
        return Promise.resolve({ data: null, error: { message: 'remove failed' }, response: new Response(null, { status: 500 }) })
      }
      removed = true
      return Promise.resolve({ data: null, error: null, response: new Response(null, { status: 204 }) })
    }),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// Renders the dialog body + title only while open — enough for these assertions,
// without reka-ui's portal (which happy-dom does not render).
const DialogStub = {
  props: ['open', 'title'],
  template: '<div v-if="open" class="dialog-stub"><h2>{{ title }}</h2><slot /><slot name="actions" /></div>',
}

/** Mounts the page and opens the remove-source dialog on the only source row. */
async function openRemoveDialog() {
  const wrapper = await mountSuspended(Page, { global: { stubs: { Dialog: DialogStub } } })
  await flushPromises()

  const removeButton = wrapper.findAll('button').find((b) => b.text() === 'Remove')
  expect(removeButton).toBeDefined()
  await removeButton!.trigger('click')
  await flushPromises()

  expect(wrapper.text()).toContain('Remove “asurascans”?')
  return wrapper
}

/** The dialog's destructive confirm button ("Remove source"). */
function confirmButton(wrapper: Awaited<ReturnType<typeof openRemoveDialog>>) {
  return wrapper.findAll('button').find((b) => b.text() === 'Remove source')
}

describe('series detail page — remove-source dialog', () => {
  beforeEach(() => {
    removeOk = true
    removed = false
  })

  it('closes the dialog once the removal succeeds', async () => {
    const wrapper = await openRemoveDialog()

    await confirmButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).not.toContain('Remove “asurascans”?')
    expect(confirmButton(wrapper)).toBeUndefined()
  })

  it('never degrades the heading to an empty name after a successful removal', async () => {
    const wrapper = await openRemoveDialog()

    await confirmButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).not.toContain('Remove “”?')
  })

  it('keeps the dialog OPEN and surfaces the error when the removal fails (§16)', async () => {
    removeOk = false
    const wrapper = await openRemoveDialog()

    await confirmButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Remove “asurascans”?')
    expect(confirmButton(wrapper)).toBeDefined()
    expect(wrapper.text()).toContain('Update failed')
  })
})
