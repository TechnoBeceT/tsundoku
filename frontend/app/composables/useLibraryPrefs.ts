/**
 * useLibraryPrefs — the server-side persistence layer for the library-list view
 * state (sort field + direction + toggle-filters).
 *
 * BEST-EFFORT by design (§16 sanctioned invisible side-effect, same class as the
 * reader's progress writes): a failed load falls back to the caller's defaults
 * and a failed save is swallowed. Persisting the owner's view preference must
 * NEVER surface an error banner or block the grid — it is an invisible
 * convenience, not a user-driven action.
 *
 * Storage is a single-owner server-side preference (GET/PUT /api/library/prefs),
 * so the chosen sort + filters survive a refresh/restart and are shared
 * cross-device — no localStorage (which would be per-device only).
 */
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'

export type LibraryPrefsDTO = components['schemas']['LibraryPrefs']

/** Trailing debounce (ms) for saves — coalesces a burst of toggle flips. */
const SAVE_DEBOUNCE_MS = 400

export function useLibraryPrefs() {
  let saveTimer: ReturnType<typeof setTimeout> | null = null

  /**
   * Load the persisted prefs. Returns null on any failure (network, non-2xx,
   * missing body) so the caller keeps its own defaults — never throws.
   */
  async function load(): Promise<LibraryPrefsDTO | null> {
    try {
      const res = await apiClient.GET('/api/library/prefs')
      if (res.error || !res.data) return null
      return res.data
    }
    catch {
      return null
    }
  }

  /**
   * Persist the prefs, debounced. A rapid sequence of changes (e.g. toggling
   * several filters) collapses to one PUT after the caller settles. Failures are
   * swallowed — the in-memory state is already applied, so a lost save only
   * means the choice doesn't survive the next reload.
   */
  function save(prefs: LibraryPrefsDTO): void {
    if (saveTimer) clearTimeout(saveTimer)
    saveTimer = setTimeout(() => {
      void apiClient.PUT('/api/library/prefs', { body: prefs }).catch(() => undefined)
    }, SAVE_DEBOUNCE_MS)
  }

  return { load, save }
}
