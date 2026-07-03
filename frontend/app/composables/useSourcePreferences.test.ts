/**
 * useSourcePreferences — load + write (§16 refresh-from-response).
 *
 * Pins:
 *   1. load(pkg) fetches GET …/preferences and exposes the grouped sources.
 *   2. setPreference PATCHes {sourceId, position, value} and REPLACES that
 *      source's list with the authoritative response (so positions stay fresh —
 *      the position-drift mitigation).
 *   3. a write failure surfaces in saveError (§16) without throwing.
 *
 * Non-vacuous: if setPreference stopped applying the PATCH response, assertion 2
 * (the switch reads back false) fails; if it swallowed errors silently, 3 fails.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSourcePreferences } from './useSourcePreferences'
import { preferenceGroup, switchPref } from '../fixtures/preferences'

// ── Mock the API client ─────────────────────────────────────────────────────────

const { getMock, patchMock } = vi.hoisted(() => ({ getMock: vi.fn(), patchMock: vi.fn() }))

vi.mock('~/utils/api/client', () => ({
  apiClient: { GET: getMock, PATCH: patchMock },
  setUnauthorizedHandler: vi.fn(),
}))

describe('useSourcePreferences', () => {
  beforeEach(() => {
    getMock.mockReset()
    patchMock.mockReset()
  })

  it('load fetches and exposes the grouped sources', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    const { groups, pending, load } = useSourcePreferences()

    await load('pkg.test')

    expect(pending.value).toBe(false)
    expect(groups.value).toHaveLength(1)
    expect(groups.value[0]!.sourceId).toBe('src-en')
    // The path param was threaded through.
    expect(getMock).toHaveBeenCalledWith('/api/suwayomi/extensions/{pkgName}/preferences', {
      params: { path: { pkgName: 'pkg.test' } },
    })
  })

  it('setPreference PATCHes and applies the refreshed list (§16)', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    // The write returns the refreshed source list with the switch now OFF.
    patchMock.mockResolvedValue({
      data: [{ ...switchPref, currentValue: false }, ...preferenceGroup.preferences.slice(1)],
      error: null,
    })

    const { groups, savingKey, load, setPreference } = useSourcePreferences()
    await load('pkg.test')

    await setPreference('src-en', 0, false)

    // The PATCH carried the exact write coordinates + value.
    expect(patchMock).toHaveBeenCalledWith('/api/suwayomi/extensions/{pkgName}/preferences', {
      params: { path: { pkgName: 'pkg.test' } },
      body: { sourceId: 'src-en', position: 0, value: false },
    })
    // The authoritative response replaced that source's list (position stays fresh).
    expect(groups.value[0]!.preferences[0]!.currentValue).toBe(false)
    expect(savingKey.value).toBeNull()
  })

  it('surfaces a write failure in saveError without throwing', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    patchMock.mockResolvedValue({ data: null, error: { message: 'Expected change to SwitchPreferenceCompat' } })

    const { saveError, savingKey, load, setPreference } = useSourcePreferences()
    await load('pkg.test')

    await setPreference('src-en', 0, true)

    expect(saveError.value).toBe('Expected change to SwitchPreferenceCompat')
    expect(savingKey.value).toBeNull()
  })
})
