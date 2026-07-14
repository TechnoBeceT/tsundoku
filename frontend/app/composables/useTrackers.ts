/**
 * useTrackers — data layer for the Settings → Trackers pane (Phase 3d).
 *
 * Fetches GET /api/trackers (every registered tracker's connect status — AniList,
 * MAL, Kitsu, MangaUpdates) and exposes the connect/login/logout mutations. Not a
 * module singleton (mirrors useExtensions/useMetadata, not useAuth) — each caller
 * (the Settings page, the series-detail page's "Add tracker" flow) gets its own
 * fresh GET on mount.
 *
 * OAuth "not configured" detection: `Tracker` carries no `configured` flag (the
 * backend deliberately keeps `GET /api/trackers` side-effect-free — see its own
 * doc comment: building an authorize URL stashes a pending PKCE login server-side,
 * so a plain status list must never trigger that). The only way to learn a
 * tracker's client-id/public-URL is unset is to ask `authUrl()` and see it fail
 * closed (400) — so `misconfigured` is a set of tracker ids LEARNED from a failed
 * `authUrl()` call, not a static property. It starts empty and only grows/shrinks
 * as the owner actually attempts to connect.
 *
 * §16 mutations (one action in flight at a time in practice, so a single shared
 * `actionBusyId`/`actionError` pair — not per-row RowActionState — keeps this
 * composable's surface small):
 *   authUrl(trackerId)                    — GET  /api/trackers/{id}/auth-url
 *   loginOAuth(trackerId, callbackUrl)     — POST /api/trackers/{id}/login/oauth
 *   loginCredentials(trackerId, user, pw)  — POST /api/trackers/{id}/login/credentials
 *   logout(trackerId)                      — POST /api/trackers/{id}/logout
 * `loginOAuth`/`loginCredentials` apply the returned, authoritative `Tracker`
 * directly into `trackers` (§16 mutate-reseeds-from-response — no extra `list()`
 * round-trip). `logout` is a 204 (no body), so it patches the row to logged-out
 * locally instead.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { TrackerStatus } from '~/components/screens/settings.types'

type TrackerDTO = components['schemas']['Tracker']

function mapTracker(dto: TrackerDTO): TrackerStatus {
  return {
    id: dto.id,
    name: dto.name,
    needsOAuth: dto.needsOAuth,
    isLoggedIn: dto.isLoggedIn,
    isTokenExpired: dto.isTokenExpired,
    username: dto.username,
  }
}

export function useTrackers() {
  const trackers = ref<TrackerStatus[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)

  // §16 state of the one in-flight connect/login/logout action.
  const actionBusyId = ref<number | null>(null)
  const actionError = ref<string | null>(null)

  // Tracker ids whose most recent authUrl() attempt revealed a missing
  // client-id / instance public-URL config (see the doc comment above).
  const misconfigured = ref<Set<number>>(new Set())

  /** Replace or append one tracker's row from an authoritative DTO (§16). */
  function applyTracker(dto: TrackerDTO): void {
    const mapped = mapTracker(dto)
    const idx = trackers.value.findIndex((t) => t.id === mapped.id)
    trackers.value = idx === -1
      ? [...trackers.value, mapped]
      : trackers.value.map((t, i) => (i === idx ? mapped : t))
  }

  /** Loads (or reloads) every registered tracker's connect status. */
  async function list(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/trackers')
      if (res.error || !res.data) throw new Error('Failed to load trackers')
      trackers.value = res.data.map(mapTracker)
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load trackers'
    }
    finally {
      pending.value = false
    }
  }

  /**
   * Builds a fresh OAuth authorize URL for trackerId. Resolves the URL on
   * success (the caller does the full-tab `window.location.href` navigate — this
   * composable never touches `window`, keeping it Node-test-friendly). On a 400
   * (no client-id / public URL configured) marks the tracker `misconfigured` and
   * resolves null; any other failure also resolves null with `actionError` set.
   */
  async function authUrl(trackerId: number): Promise<string | null> {
    actionBusyId.value = trackerId
    actionError.value = null
    try {
      const res = await apiClient.GET('/api/trackers/{id}/auth-url', { params: { path: { id: trackerId } } })
      if (res.error || !res.data) {
        misconfigured.value = new Set(misconfigured.value).add(trackerId)
        actionError.value = res.error?.message ?? 'This tracker is not configured yet.'
        return null
      }
      if (misconfigured.value.has(trackerId)) {
        const next = new Set(misconfigured.value)
        next.delete(trackerId)
        misconfigured.value = next
      }
      return res.data.authUrl
    }
    catch (err) {
      actionError.value = err instanceof Error ? err.message : 'Failed to build the authorize URL'
      return null
    }
    finally {
      actionBusyId.value = null
    }
  }

  /**
   * Completes the OAuth round trip (the callback route's own call) — POSTs the
   * full callback URL the SPA's callback route received. Resolves true/false;
   * on success the returned Tracker is applied directly (§16).
   */
  async function loginOAuth(trackerId: number, callbackUrl: string): Promise<boolean> {
    actionBusyId.value = trackerId
    actionError.value = null
    try {
      const res = await apiClient.POST('/api/trackers/{id}/login/oauth', {
        params: { path: { id: trackerId } },
        body: { callbackUrl },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Login failed')
      applyTracker(res.data)
      return true
    }
    catch (err) {
      actionError.value = err instanceof Error ? err.message : 'Login failed'
      return false
    }
    finally {
      actionBusyId.value = null
    }
  }

  /**
   * Direct username/password login (Kitsu password grant, MangaUpdates session).
   * Resolves true/false; on success the returned Tracker is applied directly (§16).
   * The password is never logged.
   */
  async function loginCredentials(trackerId: number, username: string, password: string): Promise<boolean> {
    actionBusyId.value = trackerId
    actionError.value = null
    try {
      const res = await apiClient.POST('/api/trackers/{id}/login/credentials', {
        params: { path: { id: trackerId } },
        body: { username, password },
      })
      if (res.error || !res.data) throw new Error(res.error ? res.error.message : 'Sign-in failed')
      applyTracker(res.data)
      return true
    }
    catch (err) {
      actionError.value = err instanceof Error ? err.message : 'Sign-in failed'
      return false
    }
    finally {
      actionBusyId.value = null
    }
  }

  /**
   * Disconnects trackerId's account (idempotent — 204 whether or not it was
   * connected). The endpoint returns no body, so a success patches the row to
   * logged-out locally rather than a wasted extra `list()` round-trip.
   */
  async function logout(trackerId: number): Promise<void> {
    actionBusyId.value = trackerId
    actionError.value = null
    try {
      const res = await apiClient.POST('/api/trackers/{id}/logout', { params: { path: { id: trackerId } } })
      if (res.error) throw new Error(res.error.message)
      trackers.value = trackers.value.map((t) =>
        t.id === trackerId ? { ...t, isLoggedIn: false, isTokenExpired: false, username: '' } : t)
    }
    catch (err) {
      actionError.value = err instanceof Error ? err.message : 'Disconnect failed'
    }
    finally {
      actionBusyId.value = null
    }
  }

  void list()

  return {
    trackers,
    pending,
    error,
    actionBusyId,
    actionError,
    misconfigured,
    list,
    authUrl,
    loginOAuth,
    loginCredentials,
    logout,
  }
}
