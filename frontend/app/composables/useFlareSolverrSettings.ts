/**
 * useFlareSolverrSettings — data layer for the Settings → Suwayomi pane's
 * FlareSolverr card.
 *
 * Fetches GET /api/flaresolverr/settings and maps the backend
 * FlareSolverrSettings DTO onto the screen's FlareSolverrConfig. Exposes
 * save() with the §16 SaveState lifecycle: idle → saving → success/error.
 *
 * This is TSUNDOKU-OWNED config (QCAT-238) — a SEPARATE endpoint from
 * useSuwayomiSettings.ts's SOCKS-proxy pane, even though the two render side
 * by side in SuwayomiPane.vue. The backend best-effort mirrors a save down to
 * Suwayomi's own settings; the frontend never talks to Suwayomi directly for
 * this card.
 *
 * Field renames (API → screen):
 *   sessionName      → session
 *   asResponseFallback → fallback
 *
 * Unit conversions (API → screen):
 *   timeout   (integer seconds) → DurationValue { value, unit:'s' }
 *   sessionTtl (integer minutes) → DurationValue { value, unit:'m' }
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { FlareSolverrConfig, SaveState } from '~/components/screens/settings.types'
import { fromSecondsDuration, fromMinutesDuration, toSecondsDuration, toMinutesDuration } from '~/utils/durationConversion'

type FlareSolverrSettingsDTO = components['schemas']['FlareSolverrSettings']
type FlareSolverrUpdateDTO = components['schemas']['FlareSolverrUpdate']

/** Maps the GET/PATCH response DTO onto the screen's editable config shape. */
function mapSettings(dto: FlareSolverrSettingsDTO): FlareSolverrConfig {
  return {
    enabled: dto.enabled,
    url: dto.url,
    timeout: fromSecondsDuration(dto.timeout),
    session: dto.sessionName,
    sessionTtl: fromMinutesDuration(dto.sessionTtl),
    fallback: dto.asResponseFallback,
  }
}

/** Maps the screen's editable config back onto the PATCH request DTO. */
function buildUpdate(cfg: FlareSolverrConfig): FlareSolverrUpdateDTO {
  return {
    enabled: cfg.enabled,
    url: cfg.url,
    timeout: toSecondsDuration(cfg.timeout),
    sessionName: cfg.session,
    sessionTtl: toMinutesDuration(cfg.sessionTtl),
    asResponseFallback: cfg.fallback,
  }
}

/** The default (nothing loaded yet) FlareSolverr config. */
const DEFAULT_CONFIG: FlareSolverrConfig = {
  enabled: false,
  url: '',
  timeout: { value: 60, unit: 's' },
  session: '',
  sessionTtl: { value: 15, unit: 'm' },
  fallback: false,
}

export function useFlareSolverrSettings() {
  const config = ref<FlareSolverrConfig>({ ...DEFAULT_CONFIG })
  const flareSolverrSave = ref<SaveState>({ status: 'idle' })
  const pending = ref(false)
  const error = ref<string | null>(null)

  async function refresh(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/flaresolverr/settings')
      if (res.error || !res.data) throw new Error('Failed to load FlareSolverr settings')
      config.value = mapSettings(res.data)
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load FlareSolverr settings'
    }
    finally {
      pending.value = false
    }
  }

  /**
   * §16 save: build the partial FlareSolverrUpdate from the edited config,
   * PATCH /api/flaresolverr/settings, drive flareSolverrSave through the
   * SaveState lifecycle, and reseed config from the authoritative response
   * (never the local copy). The backend best-effort mirrors the saved values
   * to Suwayomi — that mirror is invisible here, a Suwayomi-down mirror
   * failure still returns 200.
   */
  async function save(next: FlareSolverrConfig): Promise<void> {
    flareSolverrSave.value = { status: 'saving' }
    try {
      const res = await apiClient.PATCH('/api/flaresolverr/settings', {
        body: buildUpdate(next),
      })
      if (res.error) {
        const msg = (res.error as { message?: string }).message ?? 'Save failed'
        flareSolverrSave.value = { status: 'error', message: msg }
        return
      }
      if (res.data) config.value = mapSettings(res.data)
      flareSolverrSave.value = { status: 'success' }
    }
    catch (err) {
      const msg = err instanceof Error ? err.message : 'Save failed'
      flareSolverrSave.value = { status: 'error', message: msg }
    }
  }

  void refresh()

  return {
    config,
    flareSolverrSave,
    pending,
    error,
    save,
    refresh,
  }
}
