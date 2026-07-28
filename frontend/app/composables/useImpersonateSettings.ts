/**
 * useImpersonateSettings — data layer for the Settings → Server config pane's
 * impersonate-gateway card (GAP-111).
 *
 * Fetches GET /api/impersonate and GET /api/sources in parallel, mapping the
 * backend ImpersonateSettings DTO onto the screen's ImpersonateConfig and the
 * engine source list onto the picker's selectable options. Exposes save() with
 * the §16 SaveState lifecycle: idle → saving → success/error.
 *
 * This is TSUNDOKU-OWNED config — its own endpoint, distinct from the
 * FlareSolverr card that renders alongside it. The backend best-effort mirrors
 * a save down to the engine host's own impersonate config; the frontend never
 * talks to the engine directly. The DTO is already flat/camelCase with no field
 * renames or unit conversions, so the mappers are near-identity — kept explicit
 * for parity with useFlareSolverrSettings.
 *
 * 🔴 IDS ON THE WIRE, NAMES ON SCREEN (GAP-131). `config.sourceIds` holds engine
 * source IDS, and ids are the only thing ever sent. The source list exists PURELY
 * so the picker can label an id with its human name — this composable is the only
 * place the two are joined, and the join never travels: no source name is ever
 * submitted, because a name is a display string an extension update can change,
 * while the engine resolves sources by id alone.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { ImpersonateConfig, SaveState, SourceOption } from '~/components/screens/settings.types'

type ImpersonateSettingsDTO = components['schemas']['ImpersonateSettings']
type ImpersonateUpdateDTO = components['schemas']['ImpersonateUpdate']
type SourceDTO = components['schemas']['Source']

/** Maps the GET/PUT response DTO onto the screen's editable config shape. */
function mapSettings(dto: ImpersonateSettingsDTO): ImpersonateConfig {
  return {
    enabled: dto.enabled,
    url: dto.url,
    sourceIds: [...dto.sourceIds],
  }
}

/** Maps an engine Source DTO → the minimal picker option (id/name/lang). */
function mapSource(dto: SourceDTO): SourceOption {
  return { id: dto.id, name: dto.name, lang: dto.lang }
}

/**
 * Maps the screen's editable config back onto the PUT request DTO. `sourceIds`
 * is ALWAYS sent, including when empty — an empty array is the meaningful
 * "no source uses the gateway" value, and omitting it would leave a stale
 * selection in place.
 */
function buildUpdate(cfg: ImpersonateConfig): ImpersonateUpdateDTO {
  return {
    enabled: cfg.enabled,
    url: cfg.url,
    sourceIds: [...cfg.sourceIds],
  }
}

/** The default (nothing loaded yet) impersonate config. */
const DEFAULT_CONFIG: ImpersonateConfig = {
  enabled: false,
  url: '',
  sourceIds: [],
}

export function useImpersonateSettings() {
  // The explicit `sourceIds: []` detaches the array from the module-level
  // DEFAULT_CONFIG, which a bare spread would share across every call.
  const config = ref<ImpersonateConfig>({ ...DEFAULT_CONFIG, sourceIds: [] })
  const sources = ref<SourceOption[]>([])
  const impersonateSave = ref<SaveState>({ status: 'idle' })
  const pending = ref(false)
  const error = ref<string | null>(null)

  async function refresh(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const [cfgRes, srcRes] = await Promise.all([
        apiClient.GET('/api/impersonate'),
        apiClient.GET('/api/sources'),
      ])
      if (cfgRes.error || !cfgRes.data) throw new Error('Failed to load impersonate settings')
      config.value = mapSettings(cfgRes.data)
      // The source list is only the picker's LABELS — an engine that cannot list
      // its sources must not blank the saved gating set, so this never throws.
      sources.value = (srcRes.data ?? []).map(mapSource)
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
    sources,
    impersonateSave,
    pending,
    error,
    save,
    refresh,
  }
}
