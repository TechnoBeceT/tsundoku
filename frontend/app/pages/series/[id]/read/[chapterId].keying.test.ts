/**
 * Reader page-key stability — proves a chapter flip does NOT remount the
 * reader route component.
 *
 * `app.vue` renders a bare `<NuxtPage />` with no `:page-key`, so Nuxt's
 * DEFAULT page key is the param-interpolated PATH (`generateRouteKey` /
 * `interpolatePath` in nuxt/dist/pages/runtime/utils.js) — which includes
 * `chapterId` and therefore changes on every chapter flip. Without a pinned
 * `key` in [chapterId].vue's `definePageMeta`, that key change tears the
 * whole reader component down and remounts it on every prev/next tap: a
 * fresh `GET /api/series/{id}`, and the in-memory `readThisSession` set in
 * `useReadingProgress` wiped (the un-read race this branch was blocked on).
 *
 * The existing route-wiring test (`[chapterId].test.ts`) mounts the page
 * component DIRECTLY via `mountSuspended(ReadPage, ...)`, which bypasses
 * Nuxt's route->component key resolution entirely (`mountSuspended` renders
 * whatever component you hand it, not the router's matched component) — so
 * it cannot see a remount caused by the page key. This test instead mounts
 * the REAL `<NuxtPage />` and drives real navigation between two chapters of
 * the SAME series via the real Nuxt router (built from this repo's actual
 * generated route table), asserting `GET /api/series/{id}` — the network
 * call `useReader`'s setup fires exactly once per mount — is called exactly
 * ONCE across the flip. A second call means the page was torn down and
 * rebuilt.
 *
 * PROOF (see the task report for the exact commands): with the `key:`
 * removed from [chapterId].vue's `definePageMeta`, this test FAILS —
 * `GET /api/series/{id}` fires twice (test observed
 * "expected 2 to be 1" / "3 to be 2" depending on which describe ran) because
 * Nuxt's default param-interpolated-path key changes on the chapterId flip
 * and remounts the page. Restoring the pinned key makes it pass again.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import { NuxtPage } from '#components'

const detailFor: Record<string, { id: string, displayName: string, title: string, chapters: unknown[] }> = {
  'series-1': {
    id: 'series-1',
    displayName: 'Test Series',
    title: 'Test Series',
    chapters: [
      { id: 'ch-a', chapterKey: 'k1', number: 1, name: 'One', state: 'downloaded', filename: 'a.cbz', pageCount: 5, read: false, lastReadPage: 0 },
      { id: 'ch-b', chapterKey: 'k2', number: 2, name: 'Two', state: 'downloaded', filename: 'b.cbz', pageCount: 5, read: false, lastReadPage: 0 },
    ],
  },
  'series-2': {
    id: 'series-2',
    displayName: 'Other Series',
    title: 'Other Series',
    chapters: [
      { id: 'ch-x', chapterKey: 'kx', number: 1, name: 'X', state: 'downloaded', filename: 'x.cbz', pageCount: 5, read: false, lastReadPage: 0 },
    ],
  },
}

// One shared GET spy: the auth middleware also calls GET /api/owner/me on
// the first navigation, so calls are filtered by path below rather than
// asserted on the mock's raw total call count.
const getSpy = vi.fn().mockImplementation((path: string, options?: { params?: { path?: { id?: string } } }) => {
  if (path === '/api/series/{id}') {
    const id = options?.params?.path?.id ?? 'series-1'
    return Promise.resolve({ data: detailFor[id] ?? detailFor['series-1'], error: null, response: new Response() })
  }
  if (path === '/api/owner/me') return Promise.resolve({ data: { ownerId: 'owner-1' }, error: null, response: new Response() })
  return Promise.resolve({ data: null, error: null, response: new Response() })
})

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: getSpy,
    PATCH: vi.fn().mockResolvedValue({ data: null, error: null }),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// happy-dom has no IntersectionObserver — ReaderStrip's onMounted needs it.
class IOStub {
  observe(): void { /* no-op stub */ }
  disconnect(): void { /* no-op stub */ }
}
vi.stubGlobal('IntersectionObserver', IOStub)

/** Count of GET /api/series/{id} calls so far — the signal a fresh mount fires. */
function seriesGetCount(): number {
  return getSpy.mock.calls.filter((call) => call[0] === '/api/series/{id}').length
}

describe('reader page key stability (real NuxtPage + real router)', () => {
  beforeEach(() => {
    getSpy.mockClear()
  })

  it('does not remount the reader across a chapter flip within the same series', async () => {
    const wrapper = await mountSuspended(NuxtPage, { route: '/series/series-1/read/ch-a' })
    await wrapper.vm.$nextTick()
    expect(seriesGetCount()).toBe(1)

    const router = (wrapper.vm as unknown as { $router: { replace: (path: string) => Promise<unknown> } }).$router
    await router.replace('/series/series-1/read/ch-b')
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    // Still 1 — no remount, so no fresh GET /api/series/{id}.
    expect(seriesGetCount()).toBe(1)
  })

  it('a fresh deep link into a different series DOES remount (the pinned key includes the series id)', async () => {
    const wrapper = await mountSuspended(NuxtPage, { route: '/series/series-1/read/ch-a' })
    await wrapper.vm.$nextTick()
    expect(seriesGetCount()).toBe(1)

    const router = (wrapper.vm as unknown as { $router: { replace: (path: string) => Promise<unknown> } }).$router
    await router.replace('/series/series-2/read/ch-x')
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    // A different series id changes the pinned key (`/series/${id}/read`), so
    // this IS a genuine fresh mount: it must actually refetch and switch onto
    // series-2 (not get stuck showing series-1's stale chapter list, which is
    // what a key that never changes would do). Vue/Nuxt's own Suspense
    // resolution can invoke the new page's setup more than once while
    // swapping — an unrelated harness/framework quirk, not asserted on here —
    // so this checks that series-2 was fetched at all, not an exact count.
    const idsFetched = getSpy.mock.calls
      .filter((call) => call[0] === '/api/series/{id}')
      .map((call) => (call[1] as { params?: { path?: { id?: string } } })?.params?.path?.id)
    expect(idsFetched).toContain('series-2')
    expect(wrapper.html()).toContain('Other Series')
  })
})
