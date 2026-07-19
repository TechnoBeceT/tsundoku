/**
 * useSourcePreferences — data layer for the per-source "Configure" dialog.
 *
 * Loads GET /api/suwayomi/extensions/{pkgName}/preferences (an extension's
 * configurable preferences grouped by its per-language sources) and writes one
 * preference back via PATCH. A preference is KEY-addressed on write (the
 * engine host has no stable array position to index by — unlike the retired
 * Suwayomi shape); because a write can still shift other fields (e.g. a
 * ListPreference's currentValue), this composable ALWAYS replaces a source's
 * preference list with the authoritative refreshed list the PATCH returns
 * (§16) — never assuming the local copy is still correct after a write.
 *
 * It also owns the per-language enable/disable toggle: PATCH
 * /api/sources/{sourceId}/enabled hides a disabled source from Tsundoku's
 * Discover/Search/Browse pickers without touching any series already adopted
 * from it. This flag is TSUNDOKU-SIDE (the internal engine has no "disabled
 * source" concept). Like setPreference, it reseeds the group's `enabled` from
 * the authoritative response rather than optimistically flipping the local
 * flag (§16).
 *
 * It likewise owns the per-source ignore-scanlator toggle: PATCH
 * /api/sources/{sourceId}/ignore-scanlator flags an uploader-in-scanlator source
 * (e.g. Hive Scans) so future adopts collapse its per-uploader providers into one
 * [Source] provider. Also TSUNDOKU-SIDE and apply-forward only (it never migrates
 * an already-adopted series); it reseeds the group's `ignoreScanlator` from the
 * authoritative response (§16).
 *
 * Public surface:
 *   groups        — the sources + their preferences (reactive)
 *   pending       — the initial load is in flight
 *   error         — a load failure message (or null)
 *   savingKey     — `${sourceId}:${key}` of the preference being written (or null)
 *   saveError     — a write failure message (or null)
 *   enablingKey   — the sourceId whose enable/disable toggle is being written (or null)
 *   enableError   — an enable/disable write failure message (or null)
 *   ignoringKey   — the sourceId whose ignore-scanlator toggle is being written (or null)
 *   ignoreError   — an ignore-scanlator write failure message (or null)
 *   load(pkg)     — fetch an extension's preferences (opens the dialog session)
 *   setPreference(sourceId, key, value) — write one preference (§16)
 *   setEnabled(sourceId, enabled) — toggle a source's enable/disable state (§16)
 *   setIgnoreScanlator(sourceId, ignoreScanlator) — toggle a source's ignore-scanlator flag (§16)
 *   reset()       — clear all state (on dialog close)
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'

type Group = components['schemas']['SourcePreferencesGroup']
type Preference = components['schemas']['SourcePreference']
type ScanlatorMigration = components['schemas']['ScanlatorMigration']

/** The value a write can carry — a boolean, a string, or an array of strings. */
export type SourcePreferenceValue = boolean | string | string[]

/** The busy-key for a preference being written: `${sourceId}:${key}`. */
export function preferenceKey(sourceId: string, key: string): string {
  return `${sourceId}:${key}`
}

/** A migration-result banner: the message plus a tone that drives its styling. */
export interface MigrationBanner {
  message: string
  /** `success` = at least one series collapsed; `warning` = nothing collapsed. */
  tone: 'success' | 'warning'
}

/**
 * Builds the ignore-scanlator on-enable migration banner so the destructive
 * migration is never silent (§16):
 *   - merged > 0 → a SUCCESS banner ("Merged N … across M series …"), with a
 *     skipped suffix when some series could not be migrated.
 *   - merged === 0 && skipped > 0 → a WARNING banner: EVERY affected series
 *     failed, so NOTHING was relabeled — the owner must not think it worked.
 *   - merged === 0 && skipped === 0 → null (nothing to migrate, or flag OFF).
 */
export function formatMigration(migration: ScanlatorMigration | undefined): MigrationBanner | null {
  if (!migration || (migration.merged === 0 && migration.skipped === 0)) return null
  if (migration.merged === 0) {
    return {
      message: `Couldn't collapse ${migration.skipped} series — nothing was relabeled. Check the logs and try again.`,
      tone: 'warning',
    }
  }
  const providers = migration.merged === 1 ? 'provider' : 'providers'
  let message = `Merged ${migration.merged} per-uploader ${providers} across ${migration.seriesProcessed} series and relabeled their files.`
  if (migration.skipped > 0) {
    message += ` ${migration.skipped} series could not be migrated and were skipped — check the logs and try again.`
  }
  return { message, tone: 'success' }
}

export function useSourcePreferences() {
  const groups = ref<Group[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)
  const savingKey = ref<string | null>(null)
  const saveError = ref<string | null>(null)
  const enablingKey = ref<string | null>(null)
  const enableError = ref<string | null>(null)
  const ignoringKey = ref<string | null>(null)
  const ignoreError = ref<string | null>(null)
  const migrationMessage = ref<MigrationBanner | null>(null)

  // The pkgName of the currently-loaded extension (the PATCH path param).
  const pkgName = ref('')

  /** Loads an extension's per-source preferences (the dialog-open action). */
  async function load(pkg: string): Promise<void> {
    pkgName.value = pkg
    pending.value = true
    error.value = null
    saveError.value = null
    savingKey.value = null
    try {
      const res = await apiClient.GET('/api/suwayomi/extensions/{pkgName}/preferences', {
        params: { path: { pkgName: pkg } },
      })
      if (res.error || !res.data) throw new Error(res.error?.message ?? 'Failed to load preferences')
      groups.value = res.data.sources
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load preferences'
      groups.value = []
    }
    finally {
      pending.value = false
    }
  }

  /**
   * Replaces the given source's preference list with the authoritative refreshed
   * one returned by a write, so any dialog re-render always shows current state.
   */
  function applyRefreshed(sourceId: string, prefs: Preference[]): void {
    groups.value = groups.value.map(g =>
      g.sourceId === sourceId ? { ...g, preferences: prefs } : g,
    )
  }

  /**
   * Writes one preference by key. Drives savingKey (the row spinner) and
   * surfaces any failure in saveError (§16). On success applies the refreshed
   * list so the dialog reflects the engine host's authoritative state.
   */
  async function setPreference(sourceId: string, key: string, value: SourcePreferenceValue): Promise<void> {
    savingKey.value = preferenceKey(sourceId, key)
    saveError.value = null
    try {
      const res = await apiClient.PATCH('/api/suwayomi/extensions/{pkgName}/preferences', {
        params: { path: { pkgName: pkgName.value } },
        body: { sourceId, key, value },
      })
      if (res.error || !res.data) throw new Error(res.error?.message ?? 'Failed to save preference')
      applyRefreshed(sourceId, res.data)
      savingKey.value = null
    }
    catch (e) {
      saveError.value = e instanceof Error ? e.message : 'Failed to save preference'
      savingKey.value = null
    }
  }

  /**
   * Toggles a source's per-language enable/disable state. Drives enablingKey
   * (the group's Switch spinner) and surfaces any failure in enableError (§16).
   * On success applies the RE-READ enabled flag from the response, never the
   * optimistic request value.
   */
  async function setEnabled(sourceId: string, enabled: boolean): Promise<void> {
    enablingKey.value = sourceId
    enableError.value = null
    try {
      const res = await apiClient.PATCH('/api/sources/{sourceId}/enabled', {
        params: { path: { sourceId } },
        body: { enabled },
      })
      if (res.error || !res.data) throw new Error(res.error?.message ?? 'Failed to update source')
      const authoritative = res.data.enabled
      groups.value = groups.value.map(g =>
        g.sourceId === sourceId ? { ...g, enabled: authoritative } : g,
      )
      enablingKey.value = null
    }
    catch (e) {
      enableError.value = e instanceof Error ? e.message : 'Failed to update source'
      enablingKey.value = null
    }
  }

  /**
   * Toggles a source's ignore-scanlator flag. Drives ignoringKey (the toggle's
   * spinner) and surfaces any failure in ignoreError (§16). On success applies
   * the RE-READ flag from the response, never the optimistic request value. When
   * flipping ON, the response also carries the Slice-B migration summary (how many
   * already-adopted series were collapsed) — surfaced in migrationMessage so the
   * owner sees that existing files were relabeled, not just future adopts.
   */
  async function setIgnoreScanlator(sourceId: string, ignoreScanlator: boolean): Promise<void> {
    ignoringKey.value = sourceId
    ignoreError.value = null
    migrationMessage.value = null
    try {
      const res = await apiClient.PATCH('/api/sources/{sourceId}/ignore-scanlator', {
        params: { path: { sourceId } },
        body: { ignoreScanlator },
      })
      if (res.error || !res.data) throw new Error(res.error?.message ?? 'Failed to update source')
      const authoritative = res.data.ignoreScanlator
      groups.value = groups.value.map(g =>
        g.sourceId === sourceId ? { ...g, ignoreScanlator: authoritative } : g,
      )
      migrationMessage.value = formatMigration(res.data.migration)
      ignoringKey.value = null
    }
    catch (e) {
      ignoreError.value = e instanceof Error ? e.message : 'Failed to update source'
      ignoringKey.value = null
    }
  }

  /** Clears all state — call when the dialog closes to bound the session. */
  function reset(): void {
    groups.value = []
    error.value = null
    saveError.value = null
    savingKey.value = null
    enablingKey.value = null
    enableError.value = null
    ignoringKey.value = null
    ignoreError.value = null
    migrationMessage.value = null
    pkgName.value = ''
  }

  return {
    groups,
    pending,
    error,
    savingKey,
    saveError,
    enablingKey,
    enableError,
    ignoringKey,
    ignoreError,
    migrationMessage,
    load,
    setPreference,
    setEnabled,
    setIgnoreScanlator,
    reset,
  }
}
