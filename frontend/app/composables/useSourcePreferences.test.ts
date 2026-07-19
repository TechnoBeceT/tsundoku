/**
 * useSourcePreferences — load + write (§16 refresh-from-response).
 *
 * Pins:
 *   1. load(pkg) fetches GET …/preferences and exposes the grouped sources.
 *   2. setPreference PATCHes {sourceId, key, value} (KEY-addressed — the
 *      engine host has no stable array position to index by) and REPLACES
 *      that source's list with the authoritative response.
 *   3. a write failure surfaces in saveError (§16) without throwing.
 *
 * The per-language enable/disable toggle is also covered:
 *   4. setEnabled PATCHes /api/sources/{sourceId}/enabled and reseeds the
 *      group's `enabled` from the authoritative response (§16).
 *   5. an enable/disable failure surfaces in enableError without throwing.
 *
 * The per-source ignore-scanlator toggle mirrors it:
 *   6. setIgnoreScanlator PATCHes /api/sources/{sourceId}/ignore-scanlator and
 *      reseeds the group's `ignoreScanlator` from the authoritative response (§16).
 *   7. an ignore-scanlator failure surfaces in ignoreError without throwing.
 *
 * Non-vacuous: if setPreference stopped applying the PATCH response, assertion 2
 * (the switch reads back false) fails; if it swallowed errors silently, 3 fails.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { formatMigration, useSourcePreferences } from './useSourcePreferences'
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

  it('setPreference PATCHes by key and applies the refreshed list (§16)', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    // The write returns the refreshed source list with the switch now OFF.
    patchMock.mockResolvedValue({
      data: [{ ...switchPref, currentValue: false }, ...preferenceGroup.preferences.slice(1)],
      error: null,
    })

    const { groups, savingKey, load, setPreference } = useSourcePreferences()
    await load('pkg.test')

    await setPreference('src-en', 'dataSaver_en', false)

    // The PATCH carried the exact write coordinates + value.
    expect(patchMock).toHaveBeenCalledWith('/api/suwayomi/extensions/{pkgName}/preferences', {
      params: { path: { pkgName: 'pkg.test' } },
      body: { sourceId: 'src-en', key: 'dataSaver_en', value: false },
    })
    // The authoritative response replaced that source's list.
    expect(groups.value[0]!.preferences[0]!.currentValue).toBe(false)
    expect(savingKey.value).toBeNull()
  })

  it('surfaces a write failure in saveError without throwing', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    patchMock.mockResolvedValue({ data: null, error: { message: 'Expected change to SwitchPreferenceCompat' } })

    const { saveError, savingKey, load, setPreference } = useSourcePreferences()
    await load('pkg.test')

    await setPreference('src-en', 'dataSaver_en', true)

    expect(saveError.value).toBe('Expected change to SwitchPreferenceCompat')
    expect(savingKey.value).toBeNull()
  })

  it('setEnabled PATCHes the toggle and reseeds enabled from the response (§16)', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    // The write returns the authoritative post-write state: disabled.
    patchMock.mockResolvedValue({ data: { sourceId: 'src-en', enabled: false }, error: null })

    const { groups, enablingKey, load, setEnabled } = useSourcePreferences()
    await load('pkg.test')
    expect(groups.value[0]!.enabled).toBe(true)

    await setEnabled('src-en', false)

    expect(patchMock).toHaveBeenCalledWith('/api/sources/{sourceId}/enabled', {
      params: { path: { sourceId: 'src-en' } },
      body: { enabled: false },
    })
    // The authoritative response drove the local flag (not the request value alone).
    expect(groups.value[0]!.enabled).toBe(false)
    expect(enablingKey.value).toBeNull()
  })

  it('surfaces an enable/disable failure in enableError without throwing', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    patchMock.mockResolvedValue({ data: null, error: { message: 'Failed to update source' } })

    const { enableError, enablingKey, groups, load, setEnabled } = useSourcePreferences()
    await load('pkg.test')

    await setEnabled('src-en', false)

    expect(enableError.value).toBe('Failed to update source')
    expect(enablingKey.value).toBeNull()
    // The local flag is untouched on failure (no optimistic flip).
    expect(groups.value[0]!.enabled).toBe(true)
  })

  it('setIgnoreScanlator PATCHes the toggle and reseeds ignoreScanlator from the response (§16)', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    // The write returns the authoritative post-write state: flagged.
    patchMock.mockResolvedValue({ data: { sourceId: 'src-en', ignoreScanlator: true }, error: null })

    const { groups, ignoringKey, load, setIgnoreScanlator } = useSourcePreferences()
    await load('pkg.test')
    expect(groups.value[0]!.ignoreScanlator).toBe(false)

    await setIgnoreScanlator('src-en', true)

    expect(patchMock).toHaveBeenCalledWith('/api/sources/{sourceId}/ignore-scanlator', {
      params: { path: { sourceId: 'src-en' } },
      body: { ignoreScanlator: true },
    })
    // The authoritative response drove the local flag (not the request value alone).
    expect(groups.value[0]!.ignoreScanlator).toBe(true)
    expect(ignoringKey.value).toBeNull()
  })

  it('surfaces an ignore-scanlator failure in ignoreError without throwing', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    patchMock.mockResolvedValue({ data: null, error: { message: 'Failed to update source' } })

    const { ignoreError, ignoringKey, groups, load, setIgnoreScanlator } = useSourcePreferences()
    await load('pkg.test')

    await setIgnoreScanlator('src-en', true)

    expect(ignoreError.value).toBe('Failed to update source')
    expect(ignoringKey.value).toBeNull()
    // The local flag is untouched on failure (no optimistic flip).
    expect(groups.value[0]!.ignoreScanlator).toBe(false)
  })

  it('surfaces the on-enable collapse migration summary in migrationMessage', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    patchMock.mockResolvedValue({
      data: { sourceId: 'src-en', ignoreScanlator: true, migration: { seriesProcessed: 3, merged: 4, skipped: 0 } },
      error: null,
    })

    const { migrationMessage, load, setIgnoreScanlator } = useSourcePreferences()
    await load('pkg.test')
    await setIgnoreScanlator('src-en', true)

    expect(migrationMessage.value?.tone).toBe('success')
    expect(migrationMessage.value?.message).toContain('Merged 4 per-uploader providers across 3 series')
  })

  it('surfaces a total-failure migration as a WARNING banner (merged=0, skipped>0) — never silent', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    patchMock.mockResolvedValue({
      data: { sourceId: 'src-en', ignoreScanlator: true, migration: { seriesProcessed: 0, merged: 0, skipped: 3 } },
      error: null,
    })

    const { migrationMessage, load, setIgnoreScanlator } = useSourcePreferences()
    await load('pkg.test')
    await setIgnoreScanlator('src-en', true)

    expect(migrationMessage.value?.tone).toBe('warning')
    expect(migrationMessage.value?.message).toContain('nothing was relabeled')
  })

  it('leaves migrationMessage null when nothing was migrated (merged=0, skipped=0)', async () => {
    getMock.mockResolvedValue({ data: { sources: [preferenceGroup] }, error: null })
    patchMock.mockResolvedValue({
      data: { sourceId: 'src-en', ignoreScanlator: true, migration: { seriesProcessed: 0, merged: 0, skipped: 0 } },
      error: null,
    })

    const { migrationMessage, load, setIgnoreScanlator } = useSourcePreferences()
    await load('pkg.test')
    await setIgnoreScanlator('src-en', true)

    expect(migrationMessage.value).toBeNull()
  })

  it('formatMigration: success suffix, warning-only, and nothing-to-migrate', () => {
    // merged>0 with skipped>0 → success banner carrying the skipped suffix.
    const ok = formatMigration({ seriesProcessed: 2, merged: 3, skipped: 1 })
    expect(ok?.tone).toBe('success')
    expect(ok?.message).toContain('1 series could not be migrated')
    // merged===0 && skipped>0 → warning banner (total failure).
    const warn = formatMigration({ seriesProcessed: 0, merged: 0, skipped: 2 })
    expect(warn?.tone).toBe('warning')
    expect(warn?.message).toContain("Couldn't collapse 2 series")
    // Nothing to migrate / no data → no banner.
    expect(formatMigration({ seriesProcessed: 0, merged: 0, skipped: 0 })).toBeNull()
    expect(formatMigration(undefined)).toBeNull()
  })
})
