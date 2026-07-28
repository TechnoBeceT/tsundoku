/**
 * Cleanup PAGE — the LAZY per-tab load contract.
 *
 * Every tab of the Cleanup console is a library-wide scan, and the Duplicates one
 * reads every series folder on disk. So opening the console must fetch exactly ONE
 * tab's data — the one being shown — and never the other two. This is the property
 * the whole page design rests on; without it, landing on Fractionals would pay for
 * the duplicates scan every time.
 *
 * Pins:
 *   1. a default open fetches ONLY /api/library/fractionals;
 *   2. a `?tab=duplicates` deep-link fetches ONLY /api/library/duplicate-files —
 *      the deep-linked tab loads, the default one does not;
 *   3. revealing a tab fetches it, and revealing it AGAIN does not refetch (the
 *      first-reveal latch holds).
 *
 * Non-vacuous: create any composable without `{ immediate: false }` and test 1 or
 * 2 fails on the extra call; drop the loaded-set latch and test 3 fails.
 *
 * The page is mounted for real (mountSuspended) so the wiring under test is the
 * wiring that ships; only the API client and the route are faked. The two cleanup
 * dialogs are stubbed — their portals do not render in happy-dom and they are not
 * what these assertions read.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import { flushPromises } from '@vue/test-utils'
import Page from './cleanup.vue'

let getCalls: string[] = []
let routeQuery: Record<string, string> = {}

mockNuxtImport('useRoute', () => () => ({ query: routeQuery, params: {} }))

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      getCalls.push(path)
      if (path === '/api/library/duplicate-files') {
        return Promise.resolve({ data: { series: [], totalFiles: 0, totalBytes: 0 }, error: null, response: new Response() })
      }
      return Promise.resolve({ data: { series: [] }, error: null, response: new Response() })
    }),
    POST: vi.fn(() => Promise.resolve({ data: null, error: null, response: new Response() })),
    PATCH: vi.fn(() => Promise.resolve({ data: null, error: null, response: new Response() })),
    DELETE: vi.fn(() => Promise.resolve({ data: null, error: null, response: new Response() })),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

const DialogStub = { template: '<div />' }

/** The library-wide list endpoints, in tab order. */
const FRACTIONALS = '/api/library/fractionals'
const SOURCELESS = '/api/library/sourceless'
const DUPLICATES = '/api/library/duplicate-files'

async function mountPage() {
  const wrapper = await mountSuspended(Page, {
    global: { stubs: { FractionalCleanupDialog: DialogStub, SourcelessCleanupDialog: DialogStub } },
  })
  await flushPromises()
  return wrapper
}

/** Clicks the tab button whose visible label matches `label`. */
async function clickTab(wrapper: Awaited<ReturnType<typeof mountPage>>, label: string) {
  const button = wrapper.findAll('button[role="tab"]').find(b => b.text().includes(label))
  expect(button, `no tab button labelled ${label}`).toBeDefined()
  await button!.trigger('click')
  await flushPromises()
}

describe('cleanup page — lazy per-tab loading', () => {
  beforeEach(() => {
    getCalls = []
    routeQuery = {}
    sessionStorage.clear()
  })

  it('fetches only the default tab on open — never the other two scans', async () => {
    await mountPage()

    expect(getCalls).toContain(FRACTIONALS)
    expect(getCalls).not.toContain(SOURCELESS)
    expect(getCalls).not.toContain(DUPLICATES)
  })

  it('fetches only the deep-linked tab when ?tab=duplicates lands', async () => {
    routeQuery = { tab: 'duplicates' }
    await mountPage()

    expect(getCalls).toContain(DUPLICATES)
    expect(getCalls).not.toContain(FRACTIONALS)
    expect(getCalls).not.toContain(SOURCELESS)
  })

  it('loads a tab on its FIRST reveal only — returning to it does not refetch', async () => {
    const wrapper = await mountPage()
    expect(getCalls.filter(p => p === DUPLICATES)).toHaveLength(0)

    await clickTab(wrapper, 'Duplicates')
    expect(getCalls.filter(p => p === DUPLICATES)).toHaveLength(1)

    await clickTab(wrapper, 'Fractionals')
    await clickTab(wrapper, 'Duplicates')
    expect(getCalls.filter(p => p === DUPLICATES)).toHaveLength(1)
    expect(getCalls.filter(p => p === FRACTIONALS)).toHaveLength(1)
  })
})
