/**
 * useImpersonateSettings — data layer for the Settings → Server config pane's
 * impersonate-gateway card (GAP-111).
 *
 * Fetches GET /api/impersonate and maps the backend ImpersonateSettings DTO
 * onto the screen's ImpersonateConfig. Exposes save() with the §16 SaveState
 * lifecycle: idle → saving → success/error.
 *
 * This is TSUNDOKU-OWNED config — its own endpoint, distinct from the
 * FlareSolverr card that renders alongside it. The backend best-effort mirrors
 * a save down to the engine host's own impersonate config; the frontend never
 * talks to the engine directly. The DTO is already flat/camelCase with no field
 * renames or unit conversions (just `enabled` + `url`), so the mappers are
 * near-identity — kept explicit for parity with useFlareSolverrSettings.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { ImpersonateConfig, SaveState } from '~/components/screens/settings.types'

type ImpersonateSettingsDTO = components['schemas']['ImpersonateSettings']
type ImpersonateUpdateDTO = components['schemas']['ImpersonateUpdate']

/** Maps the GET/PUT response DTO onto the screen's editable config shape. */
function mapSettings(dto: ImpersonateSettingsDTO): ImpersonateConfig {
  return {
    enabled: dto.enabled,
    url: dto.url,
  }
}

/** Maps the screen's editable config back onto the PUT request DTO. */
function buildUpdate(cfg: ImpersonateConfig): ImpersonateUpdateDTO {
  return {
    enabled: cfg.enabled,
    url: cfg.url,
  }
}

/** The default (nothing loaded yet) impersonate config. */
const DEFAULT_CONFIG: ImpersonateConfig = {
  enabled: false,
  url: '',
}

export function useImpersonateSettings() {
  const config = ref<ImpersonateConfig>({ ...DEFAULT_CONFIG })
  const impersonateSave = ref<SaveState>({ status: 'idle' })
  const pending = ref(false)
  const error = ref<string | null>(null)

  async function refresh(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/impersonate')
      if (res.error || !res.data) throw new Error('Failed to load impersonate settings')
      config.value = mapSettings(res.data)
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load impersonate settings'
    }
    finally {
      pending.value = false
    }
  }

  /**
   * §16 save: build the ImpersonateUpdate from the edited config, PUT
   * /api/impersonate, drive impersonateSave through the SaveState lifecycle,
   * and reseed config from the authoritative response (never the local copy).
   * The backend best-effort mirrors the saved values to the engine host — that
   * mirror is invisible here, an engine-down mirror failure still returns 200.
   */
  async function save(next: ImpersonateConfig): Promise<void> {
    impersonateSave.value = { status: 'saving' }
    try {
      const res = await apiClient.PUT('/api/impersonate', {
        body: buildUpdate(next),
      })
      if (res.error) {
        const msg = (res.error as { message?: string }).message ?? 'Save failed'
        impersonateSave.value = { status: 'error', message: msg }
        return
      }
      if (res.data) config.value = mapSettings(res.data)
      impersonateSave.value = { status: 'success' }
    }
    catch (err) {
      const msg = err instanceof Error ? err.message : 'Save failed'
      impersonateSave.value = { status: 'error', message: msg }
    }
  }

  void refresh()

  return {
    config,
    impersonateSave,
    pending,
    error,
    save,
    refresh,
  }
}
