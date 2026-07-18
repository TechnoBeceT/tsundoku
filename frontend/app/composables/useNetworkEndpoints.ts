/**
 * useNetworkEndpoints — data layer for the Settings → Network pane's Endpoints
 * card (per-source network routing, QCAT-283 Slice 4).
 *
 * Fetches GET /api/network/endpoints and maps the generated NetworkEndpoint DTO
 * onto the screen's NetworkEndpoint. There is no field rename, but the mapper is
 * kept for parity with the other composables (and to keep the components off the
 * generated schema type). The SOCKS password is WRITE-ONLY — the backend never
 * returns it, so neither the DTO nor the screen type carries it; the editor only
 * SENDS a new one via saveEndpoint.
 *
 * §16 mutations (all drive `endpointAction` — busyId + inline error):
 *   saveEndpoint(input)  — POST (create, id=null) or PATCH (update) then refetch;
 *                          an omitted/blank password keeps the stored one.
 *   removeEndpoint(id)   — DELETE; a 409 (endpoint still referenced by a binding)
 *                          surfaces the backend message VERBATIM — it lists the
 *                          referencing source ids — so the owner sees exactly what
 *                          to unbind first.
 *
 * A create action reports its in-flight row as ADD_ACTION_ID (no id yet), an
 * update/delete as the endpoint id, so the pane can spin exactly the right row /
 * dialog and close it on the success edge.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { NetworkEndpoint, NetworkEndpointInput, RowActionState } from '~/components/screens/settings.types'
import { ADD_ACTION_ID } from '~/components/screens/settings.types'

type NetworkEndpointDTO = components['schemas']['NetworkEndpoint']
type NetworkEndpointCreateDTO = components['schemas']['NetworkEndpointCreate']

// ── DTO mapper ────────────────────────────────────────────────────────────────

/** Map the wire DTO → screen NetworkEndpoint (no password — it is write-only). */
function mapEndpoint(dto: NetworkEndpointDTO): NetworkEndpoint {
  return {
    id: dto.id,
    name: dto.name,
    kind: dto.kind,
    enabled: dto.enabled,
    host: dto.host,
    port: dto.port,
    socksVersion: dto.socksVersion,
    username: dto.username,
    url: dto.url,
    session: dto.session,
    sessionTtl: dto.sessionTtl,
    timeout: dto.timeout,
    asResponseFallback: dto.asResponseFallback,
  }
}

/**
 * Build the create/update body for the input's kind — only the relevant
 * field-group is sent. The write-only `password` is included ONLY when the owner
 * typed a non-blank value; omitting it keeps the stored password on an update
 * (and sends none on a create). Create + update bodies share this shape (every
 * field optional on the wire), so one builder serves both.
 */
function buildBody(input: NetworkEndpointInput): NetworkEndpointCreateDTO {
  const base = { name: input.name, kind: input.kind, enabled: input.enabled }
  if (input.kind === 'socks') {
    return {
      ...base,
      host: input.host,
      port: input.port,
      socksVersion: input.socksVersion,
      username: input.username,
      ...(input.password ? { password: input.password } : {}),
    }
  }
  return {
    ...base,
    url: input.url,
    session: input.session,
    sessionTtl: input.sessionTtl,
    timeout: input.timeout,
    asResponseFallback: input.asResponseFallback,
  }
}

// ── Composable ────────────────────────────────────────────────────────────────

export function useNetworkEndpoints() {
  const endpoints = ref<NetworkEndpoint[]>([])
  const pending = ref(false)
  const error = ref<string | null>(null)

  // §16 state of the one in-flight endpoint mutation (save/delete): the busy row
  // (ADD_ACTION_ID for a create, else the endpoint id) + a human-readable error.
  const endpointAction = ref<RowActionState>({ busyId: null })

  /** Load (or reload) the endpoint list. */
  async function refetch(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const res = await apiClient.GET('/api/network/endpoints')
      if (res.error || !res.data) throw new Error('Failed to load network endpoints')
      endpoints.value = res.data.map(mapEndpoint)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load network endpoints'
    }
    finally {
      pending.value = false
    }
  }

  /**
   * Create (id=null) or update an endpoint, then refetch the authoritative list
   * (§16). The busy row is ADD_ACTION_ID for a create, else the endpoint id, so
   * the pane spins the add-button / the edited dialog. On success `endpointAction`
   * clears to `{ busyId: null }`; on failure it clears busyId but sets `error`.
   */
  async function saveEndpoint(input: NetworkEndpointInput): Promise<void> {
    endpointAction.value = { busyId: input.id ?? ADD_ACTION_ID }
    try {
      const res = input.id === null
        ? await apiClient.POST('/api/network/endpoints', { body: buildBody(input) })
        : await apiClient.PATCH('/api/network/endpoints/{id}', {
            params: { path: { id: input.id } },
            body: buildBody(input),
          })
      if (res.error) throw new Error(res.error.message)
      await refetch()
      endpointAction.value = { busyId: null }
    }
    catch (e) {
      endpointAction.value = {
        busyId: null,
        error: e instanceof Error ? e.message : 'Failed to save endpoint',
      }
    }
  }

  /**
   * Delete an endpoint, then refetch (§16). A 409 (the endpoint is still
   * referenced by at least one source binding) surfaces the backend message
   * verbatim — it names the referencing sources — so the owner learns exactly
   * which sources to unbind first.
   */
  async function removeEndpoint(id: string): Promise<void> {
    endpointAction.value = { busyId: id }
    try {
      const res = await apiClient.DELETE('/api/network/endpoints/{id}', {
        params: { path: { id } },
      })
      if (res.error) throw new Error(res.error.message)
      await refetch()
      endpointAction.value = { busyId: null }
    }
    catch (e) {
      endpointAction.value = {
        busyId: null,
        error: e instanceof Error ? e.message : 'Failed to delete endpoint',
      }
    }
  }

  /** Clear a lingering endpointAction error (e.g. the owner dismissed the banner). */
  function clearActionError(): void {
    endpointAction.value = { busyId: null }
  }

  void refetch()

  return {
    endpoints,
    pending,
    error,
    endpointAction,
    refetch,
    saveEndpoint,
    removeEndpoint,
    clearActionError,
  }
}
