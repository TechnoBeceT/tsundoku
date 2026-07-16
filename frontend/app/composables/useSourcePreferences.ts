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
 * The per-language enable/disable toggle is RETIRED along with it: the
 * engine host has no server-side "disabled source" concept to proxy (the
 * old `PATCH /api/suwayomi/sources/{sourceId}/enabled` endpoint is gone).
 * Do not re-add it.
 *
 * Public surface:
 *   groups        — the sources + their preferences (reactive)
 *   pending       — the initial load is in flight
 *   error         — a load failure message (or null)
 *   savingKey     — `${sourceId}:${key}` of the preference being written (or null)
 *   saveError     — a write failure message (or null)
 *   load(pkg)     — fetch an extension's preferences (opens the dialog session)
 *   setPreference(sourceId, key, value) — write one preference (§16)
 *   reset()       — clear all state (on dialog close)
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'

type Group = components['schemas']['SourcePreferencesGroup']
type Preference = components['schemas']['SourcePreference']

/** The value a write can carry — a boolean, a string, or an array of strings. */
export type SourcePreferenceValue = boolean | string | string[]

/** The busy-key for a preference being written: `${sourceId}:${key}`. */
export function preferenceKey(sourceId: string, key: string): string {
  return `${sourceId}:${key}`
}

export function useSourcePreferences() {
  const groups = ref<Group[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)
  const savingKey = ref<string | null>(null)
  const saveError = ref<string | null>(null)

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

  /** Clears all state — call when the dialog closes to bound the session. */
  function reset(): void {
    groups.value = []
    error.value = null
    saveError.value = null
    savingKey.value = null
    pkgName.value = ''
  }

  return {
    groups,
    pending,
    error,
    savingKey,
    saveError,
    load,
    setPreference,
    reset,
  }
}
