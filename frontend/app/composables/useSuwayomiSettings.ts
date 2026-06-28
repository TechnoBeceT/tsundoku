/**
 * useSuwayomiSettings — data layer for the Settings → Suwayomi pane.
 *
 * Fetches GET /api/suwayomi/settings and maps the backend SuwayomiSettings DTO
 * onto the screen's SuwayomiConfig. Exposes save() with the §16 SaveState
 * lifecycle: idle → saving → success/error.
 *
 * Field renames (API → screen):
 *   flareSolverr.sessionName      → flareSolverr.session
 *   flareSolverr.asResponseFallback → flareSolverr.fallback
 *   socksProxy                    → socks
 *
 * Unit conversions (API → screen):
 *   flareSolverr.timeout   (integer seconds) → DurationValue { value, unit:'s' }
 *   flareSolverr.sessionTtl (integer minutes) → DurationValue { value, unit:'m' }
 *   socksProxy.version     (integer 4|5)     → string '4'|'5'
 *
 * The `database` sub-object is not exposed by the API (it is a deploy concern).
 * SuwayomiConfig.database is satisfied with an empty stub so the type compiles;
 * the DB card in SuwayomiPane.vue has been removed (owner-sanctioned, Task 4).
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { DurationValue, SuwayomiConfig, SaveState } from '~/components/screens/settings.types'

type SuwayomiSettingsDTO = components['schemas']['SuwayomiSettings']
type SuwayomiSettingsUpdateDTO = components['schemas']['SuwayomiSettingsUpdate']

// ── Unit conversion helpers ───────────────────────────────────────────────────

/** Integer seconds → DurationValue (expressed in seconds). */
function fromSeconds(s: number): DurationValue {
  return { value: s, unit: 's' }
}

/** Integer minutes → DurationValue (expressed in minutes). */
function fromMinutes(m: number): DurationValue {
  return { value: m, unit: 'm' }
}

/** DurationValue → integer seconds (for the API timeout field). */
function toSeconds(d: DurationValue): number {
  if (d.unit === 'h') return d.value * 3600
  if (d.unit === 'm') return d.value * 60
  return d.value
}

/** DurationValue → integer minutes (for the API sessionTtl field). */
function toMinutes(d: DurationValue): number {
  if (d.unit === 'h') return d.value * 60
  if (d.unit === 's') return Math.round(d.value / 60)
  return d.value
}

// ── Empty DB stub ─────────────────────────────────────────────────────────────

/**
 * The API never exposes DB backend details (a deploy concern). We satisfy the
 * SuwayomiConfig.database type requirement with an empty stub so the pane
 * compiles without the removed DB card ever being rendered.
 */
const EMPTY_DB = { type: '', url: '', username: '' }

// ── DTO mappers ───────────────────────────────────────────────────────────────

function mapSettings(dto: SuwayomiSettingsDTO): SuwayomiConfig {
  const f = dto.flareSolverr
  const s = dto.socksProxy
  return {
    database: { ...EMPTY_DB },
    flareSolverr: {
      enabled: f.enabled,
      url: f.url,
      timeout: fromSeconds(f.timeout),
      session: f.sessionName,            // API: sessionName → screen: session
      sessionTtl: fromMinutes(f.sessionTtl),
      fallback: f.asResponseFallback,    // API: asResponseFallback → screen: fallback
    },
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
  const f = cfg.flareSolverr
  const s = cfg.socks
  return {
    flareSolverr: {
      enabled: f.enabled,
      url: f.url,
      timeout: toSeconds(f.timeout),
      sessionName: f.session,            // screen: session → API: sessionName
      sessionTtl: toMinutes(f.sessionTtl),
      asResponseFallback: f.fallback,    // screen: fallback → API: asResponseFallback
    },
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
    flareSolverr: {
      enabled: false,
      url: '',
      timeout: { value: 60, unit: 's' },
      session: '',
      sessionTtl: { value: 15, unit: 'm' },
      fallback: false,
    },
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
   * §16 save: build the partial SuwayomiSettingsUpdate from the edited config,
   * PATCH /api/suwayomi/settings, drive suwayomiSave through the SaveState
   * lifecycle, and reseed config from the authoritative response (never the local
   * copy). The backend returns the refreshed settings on success; on a validation
   * or upstream error it returns { message } — surfaced verbatim.
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
