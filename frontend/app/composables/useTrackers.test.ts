/**
 * useTrackers — unit tests for the composable's API surface (Phase 3d,
 * owner-delegated: overnight-built and live-untested, so these pin the pure
 * request/response wiring the backend gate can't reach from the FE side).
 *
 * Each test is non-vacuous: it asserts a specific request PATH + params or a
 * specific state transition that would fail if the wiring regressed (e.g. a
 * misconfigured `authUrl()` call silently succeeding, or `logout()` forgetting
 * to patch the row locally after its no-body 204).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useTrackers } from './useTrackers'

const ANILIST = { id: 2, name: 'AniList', needsOAuth: true, isLoggedIn: false, isTokenExpired: false, username: '', supportsPrivate: true }
const MAL = { id: 1, name: 'MyAnimeList', needsOAuth: true, isLoggedIn: false, isTokenExpired: false, username: '', supportsPrivate: false }

let getCalls: { path: string, opts: unknown }[] = []
let postCalls: { path: string, opts: unknown }[] = []

// Controls what the next /auth-url GET resolves with — flipped per-test.
let authUrlResponse: { data: unknown, error: unknown } = { data: { authUrl: 'https://anilist.co/authorize?x=1' }, error: null }
// Controls what the next /login/oauth or /login/credentials POST resolves with.
let loginResponse: { data: unknown, error: unknown } = {
  data: { ...ANILIST, isLoggedIn: true, username: 'technobecet' },
  error: null,
}
let logoutResponse: { error: unknown } = { error: null }

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string, opts: unknown) => {
      getCalls.push({ path, opts })
      if (path === '/api/trackers') return Promise.resolve({ data: [ANILIST, MAL], error: null })
      if (path === '/api/trackers/{id}/auth-url') return Promise.resolve(authUrlResponse)
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn().mockImplementation((path: string, opts: unknown) => {
      postCalls.push({ path, opts })
      if (path === '/api/trackers/{id}/login/oauth') return Promise.resolve(loginResponse)
      if (path === '/api/trackers/{id}/login/credentials') return Promise.resolve(loginResponse)
      if (path === '/api/trackers/{id}/logout') return Promise.resolve(logoutResponse)
      return Promise.resolve({ data: null, error: null })
    }),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useTrackers', () => {
  beforeEach(() => {
    getCalls = []
    postCalls = []
    authUrlResponse = { data: { authUrl: 'https://anilist.co/authorize?x=1' }, error: null }
    loginResponse = { data: { ...ANILIST, isLoggedIn: true, username: 'technobecet' }, error: null }
    logoutResponse = { error: null }
  })

  it('list() loads every tracker from GET /api/trackers', async () => {
    const { trackers, pending } = useTrackers()
    await vi.waitFor(() => expect(pending.value).toBe(false))
    expect(trackers.value.map((t) => t.id)).toEqual([2, 1])
  })

  it('authUrl() resolves the URL and never marks the tracker misconfigured on success', async () => {
    const { authUrl, misconfigured, pending } = useTrackers()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const url = await authUrl(2)

    expect(url).toBe('https://anilist.co/authorize?x=1')
    expect(getCalls.at(-1)).toEqual({
      path: '/api/trackers/{id}/auth-url',
      opts: { params: { path: { id: 2 } } },
    })
    expect(misconfigured.value.has(2)).toBe(false)
  })

  it('authUrl() marks the tracker misconfigured and resolves null on a 400 (no client-id)', async () => {
    authUrlResponse = { data: null, error: { message: 'MAL client-id is not configured' } }
    const { authUrl, misconfigured, actionError, pending } = useTrackers()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const url = await authUrl(1)

    expect(url).toBeNull()
    expect(misconfigured.value.has(1)).toBe(true)
    expect(actionError.value).toBe('MAL client-id is not configured')
  })

  it('loginOAuth() POSTs the callback URL and applies the returned Tracker directly (§16)', async () => {
    const { loginOAuth, trackers, pending } = useTrackers()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const ok = await loginOAuth(2, 'https://tsundoku.example.com/auth/tracker/callback?code=abc&state=xyz')

    expect(ok).toBe(true)
    expect(postCalls.at(-1)).toEqual({
      path: '/api/trackers/{id}/login/oauth',
      opts: { params: { path: { id: 2 } }, body: { callbackUrl: 'https://tsundoku.example.com/auth/tracker/callback?code=abc&state=xyz' } },
    })
    // No extra GET /api/trackers round-trip — the response is applied directly.
    const trackerListCalls = getCalls.filter((c) => c.path === '/api/trackers').length
    expect(trackerListCalls).toBe(1)
    expect(trackers.value.find((t) => t.id === 2)?.isLoggedIn).toBe(true)
    expect(trackers.value.find((t) => t.id === 2)?.username).toBe('technobecet')
  })

  it('loginOAuth() resolves false and sets actionError on failure', async () => {
    loginResponse = { data: null, error: { message: 'invalid state' } }
    const { loginOAuth, actionError, pending } = useTrackers()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const ok = await loginOAuth(2, 'https://tsundoku.example.com/auth/tracker/callback?error=access_denied')

    expect(ok).toBe(false)
    expect(actionError.value).toBe('invalid state')
  })

  it('loginCredentials() never puts the password anywhere but the request body', async () => {
    loginResponse = { data: { id: 3, name: 'Kitsu', needsOAuth: false, isLoggedIn: true, isTokenExpired: false, username: 'reader', supportsPrivate: true }, error: null }
    const { loginCredentials, trackers, pending } = useTrackers()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const ok = await loginCredentials(3, 'reader', 'hunter2')

    expect(ok).toBe(true)
    expect(postCalls.at(-1)).toEqual({
      path: '/api/trackers/{id}/login/credentials',
      opts: { params: { path: { id: 3 } }, body: { username: 'reader', password: 'hunter2' } },
    })
    // Kitsu wasn't in the initial list — applyTracker appends it.
    expect(trackers.value.find((t) => t.id === 3)?.isLoggedIn).toBe(true)
  })

  it('logout() patches the row to logged-out locally instead of refetching (204 has no body)', async () => {
    loginResponse = { data: { ...ANILIST, isLoggedIn: true, username: 'technobecet' }, error: null }
    const { loginOAuth, logout, trackers, pending } = useTrackers()
    await vi.waitFor(() => expect(pending.value).toBe(false))
    await loginOAuth(2, 'https://x/callback?code=a&state=b')
    expect(trackers.value.find((t) => t.id === 2)?.isLoggedIn).toBe(true)

    const trackerListCallsBefore = getCalls.filter((c) => c.path === '/api/trackers').length
    await logout(2)

    const row = trackers.value.find((t) => t.id === 2)
    expect(row?.isLoggedIn).toBe(false)
    expect(row?.username).toBe('')
    // No extra list() round-trip — the row is patched in place.
    expect(getCalls.filter((c) => c.path === '/api/trackers').length).toBe(trackerListCallsBefore)
  })
})
