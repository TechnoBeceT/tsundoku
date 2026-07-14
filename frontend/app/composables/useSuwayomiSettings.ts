/**
 * useSuwayomiSettings — data layer for the Settings → Suwayomi pane's SOCKS
 * proxy card (+ the read-only DB display).
 *
 * Fetches GET /api/suwayomi/settings and maps the backend SuwayomiSettings DTO
 * onto the screen's SuwayomiConfig. Exposes save() with the §16 SaveState
 * lifecycle: idle → saving → success/error.
 *
 * FlareSolverr moved OFF this composable (QCAT-238, 2026-07-14): it is now
 * Tsundoku-owned config served by its own endpoint — see
 * useFlareSolverrSettings.ts, wired as a SEPARATE card + save action in
 * SuwayomiPane.vue. This composable's PATCH only ever sends the socksProxy
 * group (the backend's FlareSolverr group is left entirely untouched — a nil
 * group in the partial update, never clobbered).
 *
 * Unit conversions (API → screen):
 *   socksProxy.version — integer 4|5 → screen string '4'|'5'
 *
 * The `database` sub-object is not exposed by the API (it is a deploy concern).
 * SuwayomiConfig.database is satisfied with an empty stub so the type compiles;
 * the DB card in SuwayomiPane.vue has been removed (owner-sanctioned, Task 4).
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { SuwayomiConfig, SaveState } from '~/components/screens/settings.types'

type SuwayomiSettingsDTO = components['schemas']['SuwayomiSettings']
type SuwayomiSettingsUpdateDTO = components['schemas']['SuwayomiSettingsUpdate']

// ── Empty DB stub ─────────────────────────────────────────────────────────────

/**
 * The API never exposes DB backend details (a deploy concern). We satisfy the
 * SuwayomiConfig.database type requirement with an empty stub so the pane
 * compiles without the removed DB card ever being rendered.
 */
const EMPTY_DB = { type: '', url: '', username: '' }

// ── DTO mappers ───────────────────────────────────────────────────────────────

function mapSettings(dto: SuwayomiSettingsDTO): SuwayomiConfig {
  const s = dto.socksProxy
  return {
    database: { ...EMPTY_DB },
    socks: {
      enabled: s.enabled,
      version: String(s.version),        // API: 4|5 integer → screen: '4'|'5' string
      host: s.host,
      port: s.port,
      username: s.username,
      password: s.password,
    },
  }
}

function buildUpdate(cfg: SuwayomiConfig): SuwayomiSettingsUpdateDTO {
  const s = cfg.socks
  return {
    socksProxy: {
      enabled: s.enabled,
      version: s.version === '4' ? 4 : 5,
      host: s.host,
      port: s.port,
      username: s.username,
      password: s.password,
    },
  }
}

// ── Composable ────────────────────────────────────────────────────────────────

export function useSuwayomiSettings() {
  const config = ref<SuwayomiConfig>({
    database: { ...EMPTY_DB },
    socks: {
      enabled: false,
      version: '5',
      host: '',
      port: '',
      username: '',
      password: '',
    },
  })
  const suwayomiSave = ref<SaveState>({ status: 'idle' })
  const pending = ref(false)
  const error = ref<string | null>(null)

  async function refresh(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/suwayomi/settings')
      if (res.error || !res.data) throw new Error('Failed to load Suwayomi settings')
      config.value = mapSettings(res.data)
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load Suwayomi settings'
    }
    finally {
      pending.value = false
    }
  }

  /**
   * §16 save: build the partial SuwayomiSettingsUpdate (socksProxy ONLY —
   * flareSolverr is omitted, so the backend's FlareSolverr group is never
   * touched by this save) from the edited config, PATCH
   * /api/suwayomi/settings, drive suwayomiSave through the SaveState
   * lifecycle, and reseed config from the authoritative response (never the
   * local copy). The backend returns the refreshed settings on success; on a
   * validation or upstream error it returns { message } — surfaced verbatim.
   */
  async function save(next: SuwayomiConfig): Promise<void> {
    suwayomiSave.value = { status: 'saving' }
    try {
      const res = await apiClient.PATCH('/api/suwayomi/settings', {
        body: buildUpdate(next),
      })
      if (res.error) {
        // openapi-fetch parses the error body; the backend emits { message: "..." }.
        const msg = (res.error as { message?: string }).message ?? 'Save failed'
        suwayomiSave.value = { status: 'error', message: msg }
        return
      }
      // §16: use the response body (authoritative server state), not the local copy.
      if (res.data) config.value = mapSettings(res.data)
      suwayomiSave.value = { status: 'success' }
    }
    catch (err) {
      const msg = err instanceof Error ? err.message : 'Save failed'
      suwayomiSave.value = { status: 'error', message: msg }
    }
  }

  void refresh()

  return {
    config,
    suwayomiSave,
    pending,
    error,
    save,
    refresh,
  }
}
