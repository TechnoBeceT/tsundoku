/**
 * useSourceNetworkBindings — data layer for the Settings → Network pane's
 * per-source assignment table (per-source network routing, QCAT-283 Slice 4).
 *
 * Fetches the engine source list (GET /api/sources) and the per-source bindings
 * (GET /api/network/bindings) in parallel, mapping each onto its screen type. A
 * source WITHOUT a binding row uses the global default — its absence IS the
 * signal, so the assignment table renders every source but only some carry a
 * binding.
 *
 * §16 mutations (both drive `bindingAction` — the busy sourceId + inline error):
 *   setBinding(sourceId, update) — PUT the source's binding, then refetch.
 *   clearBinding(sourceId)       — DELETE the binding, reverting to the global
 *                                  default, then refetch.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { NetworkSource, RowActionState, SourceBinding } from '~/components/screens/settings.types'

type SourceDTO = components['schemas']['Source']
type SourceNetworkBindingDTO = components['schemas']['SourceNetworkBinding']
type SourceNetworkBindingUpdateDTO = components['schemas']['SourceNetworkBindingUpdate']

// ── DTO mappers ─────────────────────────────────────────────────────────────

/** Map an engine Source DTO → the minimal screen NetworkSource (id/name/lang). */
function mapSource(dto: SourceDTO): NetworkSource {
  return { id: dto.id, name: dto.name, lang: dto.lang }
}

/** Map a binding DTO → screen SourceBinding (nullable endpoint ids: absent → null). */
function mapBinding(dto: SourceNetworkBindingDTO): SourceBinding {
  return {
    sourceId: dto.sourceId,
    socksEndpointId: dto.socksEndpointId ?? null,
    flareMode: dto.flareMode,
    flareEndpointId: dto.flareEndpointId ?? null,
  }
}

// ── Composable ────────────────────────────────────────────────────────────────

export function useSourceNetworkBindings() {
  const sources = ref<NetworkSource[]>([])
  const bindings = ref<SourceBinding[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)

  // §16 state of the one in-flight binding mutation: the busy sourceId + error.
  const bindingAction = ref<RowActionState>({ busyId: null })

  /** Load (or reload) the source list + the per-source bindings in parallel. */
  async function refetch(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const [srcRes, bindRes] = await Promise.all([
        apiClient.GET('/api/sources'),
        apiClient.GET('/api/network/bindings'),
      ])
      if (srcRes.error || !srcRes.data) throw new Error('Failed to load sources')
      if (bindRes.error || !bindRes.data) throw new Error('Failed to load network bindings')
      sources.value = srcRes.data.map(mapSource)
      bindings.value = bindRes.data.map(mapBinding)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load source bindings'
    }
    finally {
      pending.value = false
    }
  }

  /**
   * Upsert one source's binding (PUT), then refetch the authoritative table
   * (§16). `bindingAction.busyId` holds the source id while it runs; a failure
   * (e.g. an inconsistent flareMode/endpoint pair) lands in `bindingAction.error`.
   */
  async function setBinding(sourceId: string, update: SourceNetworkBindingUpdateDTO): Promise<void> {
    bindingAction.value = { busyId: sourceId }
    try {
      const res = await apiClient.PUT('/api/network/sources/{sourceId}/binding', {
        params: { path: { sourceId } },
        body: update,
      })
      if (res.error) throw new Error(res.error.message)
      await refetch()
      bindingAction.value = { busyId: null }
    }
    catch (e) {
      bindingAction.value = {
        busyId: null,
        error: e instanceof Error ? e.message : 'Failed to update binding',
      }
    }
  }

  /**
   * Clear a source's binding (DELETE), reverting it to the global default, then
   * refetch (§16). `bindingAction.busyId` holds the source id while it runs.
   */
  async function clearBinding(sourceId: string): Promise<void> {
    bindingAction.value = { busyId: sourceId }
    try {
      const res = await apiClient.DELETE('/api/network/sources/{sourceId}/binding', {
        params: { path: { sourceId } },
      })
      if (res.error) throw new Error(res.error.message)
      await refetch()
      bindingAction.value = { busyId: null }
    }
    catch (e) {
      bindingAction.value = {
        busyId: null,
        error: e instanceof Error ? e.message : 'Failed to clear binding',
      }
    }
  }

  void refetch()

  return {
    sources,
    bindings,
    pending,
    error,
    bindingAction,
    refetch,
    setBinding,
    clearBinding,
  }
}
