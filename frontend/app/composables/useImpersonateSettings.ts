/**
 * useImpersonateSettings — data layer for Download engine's global
 * impersonate-gateway card (GAP-111).
 *
 * Fetches GET /api/impersonate and GET /api/sources in parallel, mapping the
 * backend ImpersonateSettings DTO onto the screen's ImpersonateConfig and the
 * engine source list onto canonical source-catalog options. Exposes save() for the gateway-wide
 * enabled/url pair with the §16 SaveState lifecycle: idle → saving →
 * success/error. Per-source membership writes use the narrow image-proxy
 * endpoint owned by useSourceEffectiveConfiguration.
 *
 * This is TSUNDOKU-OWNED config — its own endpoint, distinct from the
 * FlareSolverr card that renders alongside it. The backend best-effort mirrors
 * a save down to the engine host's own impersonate config; the frontend never
 * talks to the engine directly. The DTO is already flat/camelCase with no field
 * renames or unit conversions, so the mappers are near-identity — kept explicit
 * for parity with useFlareSolverrSettings.
 *
 * 🔴 IDS ON THE WIRE, NAMES ON SCREEN (GAP-131). `config.sourceIds` reflects
 * engine source IDs returned by the read endpoint. It is never included in this
 * composable's global save; source membership changes are one-ID-at-a-time.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { ImpersonateConfig, SaveState, SourceOption } from '~/components/screens/settings.types'

type ImpersonateSettingsDTO = components['schemas']['ImpersonateSettings']
type ImpersonateUpdateDTO = components['schemas']['ImpersonateUpdate']
type SourceDTO = components['schemas']['Source']
type ErrorResponse = components['schemas']['ErrorResponse']
type ImpersonateGatewayUpdate = Pick<ImpersonateUpdateDTO, 'enabled' | 'url'>

/** Maps the GET/PUT response DTO onto the screen's editable config shape. */
function mapSettings(dto: ImpersonateSettingsDTO): ImpersonateConfig {
  return {
    enabled: dto.enabled,
    url: dto.url,
    sourceIds: [...dto.sourceIds],
  }
}

/** Maps an engine Source DTO → the minimal source-catalog option (id/name/lang). */
function mapSource(dto: SourceDTO): SourceOption {
  return { id: dto.id, name: dto.name, lang: dto.lang }
}

/**
 * Maps the screen's editable gateway config back onto the PUT request DTO.
 * Source membership is intentionally omitted: it is owned by the narrow
 * per-source image-proxy endpoint so one edit cannot replace the whole set.
 */
function buildUpdate(cfg: ImpersonateGatewayUpdate): ImpersonateUpdateDTO {
  return {
    enabled: cfg.enabled,
    url: cfg.url,
  }
}

/** The default (nothing loaded yet) impersonate config. */
const DEFAULT_CONFIG: ImpersonateConfig = {
  enabled: false,
  url: '',
  sourceIds: [],
}

function messageOf(error: ErrorResponse | undefined, fallback: string): string {
  return error?.message ?? fallback
}

export function useImpersonateSettings() {
  // The explicit `sourceIds: []` detaches the array from the module-level
  // DEFAULT_CONFIG, which a bare spread would share across every call.
  const config = ref<ImpersonateConfig>({ ...DEFAULT_CONFIG, sourceIds: [] })
  const sources = ref<SourceOption[]>([])
  const impersonateSave = ref<SaveState>({ status: 'idle' })
  const pending = ref(false)
  const error = ref<string | null>(null)
  const catalogPending = ref(false)
  const catalogLoaded = ref(false)
  const catalogError = ref<string | null>(null)

  async function refreshConfig(): Promise<void> {
    error.value = null
    try {
      const cfgRes = await apiClient.GET('/api/impersonate')
      if (cfgRes.error || !cfgRes.data) throw new Error('Failed to load impersonate settings')
      config.value = mapSettings(cfgRes.data)
    }
    catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to load impersonate settings'
    }
  }

  async function refreshCatalog(): Promise<void> {
    catalogPending.value = true
    catalogError.value = null
    try {
      const result = await apiClient.GET('/api/sources')
      if (result.error || !result.data) {
        throw new Error(messageOf(result.error, 'Source catalog could not be loaded.'))
      }
      sources.value = result.data.map(mapSource)
      catalogLoaded.value = true
    }
    catch (err) {
      catalogError.value = err instanceof Error ? err.message : 'Source catalog could not be loaded.'
    }
    finally {
      catalogPending.value = false
    }
  }

  async function refresh(): Promise<void> {
    pending.value = true
    await Promise.all([refreshConfig(), refreshCatalog()])
    pending.value = false
  }

  /**
   * §16 save: build the ImpersonateUpdate from the edited config, PUT
   * /api/impersonate, drive impersonateSave through the SaveState lifecycle,
   * and reseed config from the authoritative response (never the local copy).
   * The backend best-effort mirrors the saved values to the engine host — that
   * mirror is invisible here, an engine-down mirror failure still returns 200.
   */
  async function save(next: ImpersonateGatewayUpdate): Promise<void> {
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
    catalogPending,
    catalogLoaded,
    catalogError,
    save,
    refresh,
    refreshCatalog,
  }
}
