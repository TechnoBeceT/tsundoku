/**
 * useNotifications — permission/subscribe/degrade + global-toggle round-trip.
 *
 * Pins:
 *   1. Unsupported browser (no PushManager) → state 'unsupported', enable() is a
 *      no-op returning false.
 *   2. Permission denied → state 'blocked', no subscribe/POST.
 *   3. Permission granted → subscribe + POST /api/push/subscriptions, state 'granted'.
 *   4. disable() → unsubscribe + DELETE /api/push/subscriptions, state 'default'.
 *   5. setGlobal(false) → PATCH /api/settings and globalEnabled flips.
 *
 * Non-vacuous: if enable() skipped the POST, assertion 3's postCount stays 0; if
 * disable() skipped the DELETE, deleteCount stays 0; if setGlobal didn't PATCH,
 * patchCount stays 0.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useNotifications } from './useNotifications'

// ── API mock ────────────────────────────────────────────────────────────────

let getVapidError = false
let postError = false
let patchError = false
const calls = { post: 0, delete: 0, patch: 0 }

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/settings') {
        return Promise.resolve({ data: [{ key: 'notifications.enabled', value: 'true' }], error: null })
      }
      if (path === '/api/push/vapid-key') {
        if (getVapidError) return Promise.resolve({ data: null, error: { message: 'no key' } })
        return Promise.resolve({ data: { key: 'YWJjZA' }, error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn().mockImplementation(() => {
      calls.post++
      return Promise.resolve(postError ? { data: null, error: { message: 'boom' } } : { data: null, error: null })
    }),
    DELETE: vi.fn().mockImplementation(() => {
      calls.delete++
      return Promise.resolve({ data: null, error: null })
    }),
    PATCH: vi.fn().mockImplementation(() => {
      calls.patch++
      return Promise.resolve(patchError ? { data: null, error: { message: 'boom' } } : { data: null, error: null })
    }),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// ── Browser globals ───────────────────────────────────────────────────────────

const fakeSub = {
  endpoint: 'https://push.example/abc',
  toJSON: () => ({ endpoint: 'https://push.example/abc', keys: { p256dh: 'pk', auth: 'ak' } }),
  unsubscribe: vi.fn(() => Promise.resolve(true)),
}
const pushManager = {
  subscribe: vi.fn(() => Promise.resolve(fakeSub)),
  getSubscription: vi.fn(() => Promise.resolve(fakeSub)),
}

function installGlobals(opts: { supported: boolean, permission: NotificationPermission, requestResult: NotificationPermission }) {
  if (opts.supported) {
    ;(window as unknown as { PushManager: unknown }).PushManager = {}
    Object.defineProperty(navigator, 'serviceWorker', {
      configurable: true,
      value: { ready: Promise.resolve({ pushManager }) },
    })
  }
  else {
    delete (window as unknown as { PushManager?: unknown }).PushManager
  }
  // The composable only reads Notification.permission + requestPermission (never
  // constructs one), so a plain object satisfies both the feature-detect and use.
  ;(globalThis as unknown as { Notification: unknown }).Notification = {
    permission: opts.permission,
    requestPermission: vi.fn(() => Promise.resolve(opts.requestResult)),
  }
}

beforeEach(() => {
  getVapidError = false
  postError = false
  patchError = false
  calls.post = 0
  calls.delete = 0
  calls.patch = 0
  pushManager.subscribe.mockClear()
  pushManager.getSubscription.mockClear()
  fakeSub.unsubscribe.mockClear()
})

afterEach(() => {
  delete (window as unknown as { PushManager?: unknown }).PushManager
  // @ts-expect-error — remove the stubbed serviceWorker accessor between tests.
  delete navigator.serviceWorker
})

describe('useNotifications', () => {
  it('reports unsupported when PushManager is absent', async () => {
    installGlobals({ supported: false, permission: 'default', requestResult: 'default' })
    const { state, enable } = useNotifications()
    expect(state.value).toBe('unsupported')
    expect(await enable()).toBe(false)
    expect(calls.post).toBe(0)
  })

  it('reports blocked when permission is denied', async () => {
    installGlobals({ supported: true, permission: 'default', requestResult: 'denied' })
    const { state, enable } = useNotifications()
    expect(state.value).toBe('default')
    expect(await enable()).toBe(false)
    expect(state.value).toBe('blocked')
    expect(calls.post).toBe(0)
  })

  it('subscribes + POSTs when permission is granted', async () => {
    installGlobals({ supported: true, permission: 'default', requestResult: 'granted' })
    const { state, enable } = useNotifications()
    expect(await enable()).toBe(true)
    expect(pushManager.subscribe).toHaveBeenCalledTimes(1)
    expect(calls.post).toBe(1)
    expect(state.value).toBe('granted')
  })

  it('unsubscribes + DELETEs on disable', async () => {
    installGlobals({ supported: true, permission: 'granted', requestResult: 'granted' })
    const { state, disable } = useNotifications()
    expect(state.value).toBe('granted')
    expect(await disable()).toBe(true)
    expect(fakeSub.unsubscribe).toHaveBeenCalledTimes(1)
    expect(calls.delete).toBe(1)
    expect(state.value).toBe('default')
  })

  it('PATCHes the global setting on setGlobal', async () => {
    installGlobals({ supported: true, permission: 'default', requestResult: 'default' })
    const { globalEnabled, setGlobal } = useNotifications()
    expect(await setGlobal(false)).toBe(true)
    expect(calls.patch).toBe(1)
    expect(globalEnabled.value).toBe(false)
  })
})
