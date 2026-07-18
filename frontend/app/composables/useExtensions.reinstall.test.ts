/**
 * useExtensions – reversible updates: cachedVersions mapping + reinstall.
 *
 * Pins two behaviours of the reversible-update feature:
 *   1. The DTO mapper carries cachedVersions (+ versionCode) onto the screen
 *      Extension, so the version-history UI has data.
 *   2. reinstallExtension(id, versionCode) POSTs
 *      /api/suwayomi/extensions/{pkgName}/reinstall with { versionCode } and
 *      applies the authoritative §16 list the backend returns.
 *
 * Non-vacuous: if mapExtension dropped cachedVersions, assertion 1 fails on
 * undefined; if reinstall didn't POST (or sent the wrong body), the captured
 * request assertions fail; if it didn't apply the response, the extension's
 * installed version wouldn't change to the rolled-back build.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useExtensions } from './useExtensions'

// ── Fixtures ──────────────────────────────────────────────────────────────────

// Asura installed at v49 with a held rollback history (49 current, 48, 47).
const ASURA_V49 = {
  pkgName: 'asura', name: 'Asura', lang: 'en', versionName: '1.4.9', versionCode: 49,
  iconUrl: '', repoUrl: null, isInstalled: true, hasUpdate: false, isNsfw: false, sources: [],
  cachedVersions: [
    { versionCode: 49, versionName: '1.4.9', cachedAt: '2026-07-15T00:00:00Z' },
    { versionCode: 48, versionName: '1.4.8', cachedAt: '2026-06-28T00:00:00Z' },
    { versionCode: 47, versionName: '1.4.7', cachedAt: '2026-06-02T00:00:00Z' },
  ],
}
// The authoritative list the reinstall returns: Asura now running v48 (rolled back).
const ASURA_V48 = { ...ASURA_V49, versionName: '1.4.8', versionCode: 48 }

// ── Call tracking ─────────────────────────────────────────────────────────────

let reinstallBody: unknown = null
let reinstallPkg: string | null = null
let reinstallError = false

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockImplementation((path: string) => {
      if (path === '/api/suwayomi/extensions') {
        return Promise.resolve({ data: [ASURA_V49], error: null })
      }
      if (path === '/api/suwayomi/extensions/repos') {
        return Promise.resolve({ data: { repos: [] }, error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    POST: vi.fn().mockImplementation((path: string, opts: { params?: { path?: { pkgName?: string } }, body?: unknown }) => {
      if (path === '/api/suwayomi/extensions/{pkgName}/reinstall') {
        reinstallPkg = opts.params?.path?.pkgName ?? null
        reinstallBody = opts.body
        if (reinstallError) return Promise.resolve({ data: null, error: { message: 'no cached apk for that extension version' } })
        return Promise.resolve({ data: [ASURA_V48], error: null })
      }
      return Promise.resolve({ data: null, error: null })
    }),
    PATCH: vi.fn(),
    DELETE: vi.fn(),
    PUT: vi.fn(),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('useExtensions – reversible updates', () => {
  beforeEach(() => {
    reinstallBody = null
    reinstallPkg = null
    reinstallError = false
  })

  it('maps cachedVersions + versionCode onto the screen Extension', async () => {
    const { extensions, pending } = useExtensions()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    const asura = extensions.value.find(e => e.id === 'asura')!
    expect(asura.versionCode).toBe(49)
    expect(asura.cachedVersions).toHaveLength(3)
    expect(asura.cachedVersions[1]).toEqual({ versionCode: 48, versionName: '1.4.8', cachedAt: '2026-06-28T00:00:00Z' })
  })

  it('reinstallExtension POSTs { versionCode } and applies the §16 list', async () => {
    const { extensions, reinstallExtension, pending } = useExtensions()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    await reinstallExtension('asura', 48)

    expect(reinstallPkg).toBe('asura')
    expect(reinstallBody).toEqual({ versionCode: 48 })
    // The authoritative response was applied: Asura now runs the rolled-back build.
    const asura = extensions.value.find(e => e.id === 'asura')!
    expect(asura.versionCode).toBe(48)
    expect(asura.version).toBe('1.4.8')
  })

  it('surfaces a reinstall failure in extensionAction.error', async () => {
    reinstallError = true
    const { reinstallExtension, extensionAction, pending } = useExtensions()
    await vi.waitFor(() => expect(pending.value).toBe(false))

    await reinstallExtension('asura', 40)

    expect(extensionAction.value.busyId).toBeNull()
    expect(extensionAction.value.error).toBe('no cached apk for that extension version')
  })
})
