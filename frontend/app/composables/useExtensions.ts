/**
 * useExtensions — data layer for the Settings → Extensions pane.
 *
 * Fetches GET /api/suwayomi/extensions and GET /api/suwayomi/extensions/repos
 * in parallel; maps the generated Extension DTO → screen Extension with RENAMES:
 *   pkgName     → id       (pkgName is the stable identity + mutation target)
 *   versionName → version
 * Splits the flat list into `extensions` (isInstalled=true) and
 * `availableExtensions` (isInstalled=false). Maps the repo URL list to screen
 * Repo rows ({ id: url, url, isDefault: false }) — no id/isDefault concept exists
 * in the API; id equals the URL, isDefault is a presentational constant.
 *
 * §16 mutations (per-row busy via extensionAction / repoAction):
 *   installExtension(id)         — POST /api/suwayomi/extensions/{id}/install
 *   updateExtension(id)          — POST /api/suwayomi/extensions/{id}/update
 *   uninstallExtension(id)       — DELETE /api/suwayomi/extensions/{id}
 *   checkUpdates()               — POST /api/suwayomi/extensions/refresh
 *   addRepo(url)                 — appends + PUT full updated list
 *   removeRepo(id)               — filters out (id===url) + PUT full updated list
 *   reorderRepo({id, direction}) — swaps with neighbor + PUT full updated list
 *
 * extCheckInterval KNOWN GAP: there is no backend settings key for an auto-
 * extension-update-check cadence — extension refresh is MANUAL-ONLY via the
 * checkUpdates action (POST /refresh). The exported constant is a neutral
 * placeholder for the read-only DurationInput in the Repositories tab and is
 * never submitted to the API.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'
import type { components } from '~/utils/api/schema.d.ts'
import type { DurationValue, Extension, Repo, ReorderDirection, RowActionState } from '~/components/screens/settings.types'
import { ADD_ACTION_ID } from '~/components/screens/settings.types'
import { isHttpUrl } from '~/utils/safeUrl'

type ExtensionDTO = components['schemas']['Extension']

// ── DTO mapper ────────────────────────────────────────────────────────────────

function mapExtension(dto: ExtensionDTO): Extension {
  return {
    id: dto.pkgName,
    name: dto.name,
    lang: dto.lang,
    version: dto.versionName,
    hasUpdate: dto.hasUpdate,
  }
}

// ── Composable ────────────────────────────────────────────────────────────────

export function useExtensions() {
  const extensions = ref<Extension[]>([])
  const availableExtensions = ref<Extension[]>([])
  const repos = ref<Repo[]>([])

  const pending = ref(false)
  const error = ref<string | null>(null)

  const extensionAction = ref<RowActionState>({ busyId: null })
  const repoAction = ref<RowActionState>({ busyId: null })
  const checkingUpdates = ref(false)

  // extCheckInterval — KNOWN GAP: no backend key for an auto extension-update-check
  // cadence (refresh is manual-only via checkUpdates). Neutral placeholder for the
  // read-only DurationInput in the Repositories tab — never submitted to the API.
  const extCheckInterval: DurationValue = { value: 0, unit: 'h' }

  // ── Internal helpers ────────────────────────────────────────────────────────

  /** Split and apply a full Extension DTO list from the backend. */
  function applyExtensionDTOs(dtos: ExtensionDTO[]): void {
    extensions.value = dtos.filter(d => d.isInstalled).map(mapExtension)
    availableExtensions.value = dtos.filter(d => !d.isInstalled).map(mapExtension)
  }

  /** Apply an authoritative repo URL list from the backend. */
  function applyRepoUrls(urls: string[]): void {
    repos.value = urls.map(url => ({ id: url, url, isDefault: false }))
  }

  // ── Load ────────────────────────────────────────────────────────────────────

  async function refresh(): Promise<void> {
    pending.value = true
    error.value = null
    try {
      const [extRes, repoRes] = await Promise.all([
        apiClient.GET('/api/suwayomi/extensions'),
        apiClient.GET('/api/suwayomi/extensions/repos'),
      ])
      if (extRes.error || !extRes.data) throw new Error('Failed to load extensions')
      if (repoRes.error || !repoRes.data) throw new Error('Failed to load repos')
      applyExtensionDTOs(extRes.data)
      applyRepoUrls(repoRes.data.repos)
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load extensions'
    }
    finally {
      pending.value = false
    }
  }

  // ── Extension mutations ─────────────────────────────────────────────────────

  /**
   * Internal helper for install/update/uninstall. Sets busyId, awaits the
   * backend call (which returns the authoritative §16 Extension[] list),
   * applies it, then clears state. Surfaces any failure in extensionAction.error.
   */
  async function extMutate(
    busyId: string,
    fn: () => Promise<ExtensionDTO[]>,
  ): Promise<void> {
    extensionAction.value = { busyId }
    try {
      const dtos = await fn()
      applyExtensionDTOs(dtos)
      extensionAction.value = { busyId: null }
    }
    catch (e) {
      extensionAction.value = {
        busyId: null,
        error: e instanceof Error ? e.message : 'Action failed',
      }
    }
  }

  /** Installs an extension by pkgName (= id). Returns the refreshed full list (§16). */
  async function installExtension(id: string): Promise<void> {
    await extMutate(id, async () => {
      const res = await apiClient.POST('/api/suwayomi/extensions/{pkgName}/install', {
        params: { path: { pkgName: id } },
      })
      if (res.error) throw new Error(res.error.message)
      return res.data
    })
  }

  /** Updates an installed extension by pkgName. Returns the refreshed full list (§16). */
  async function updateExtension(id: string): Promise<void> {
    await extMutate(id, async () => {
      const res = await apiClient.POST('/api/suwayomi/extensions/{pkgName}/update', {
        params: { path: { pkgName: id } },
      })
      if (res.error) throw new Error(res.error.message)
      return res.data
    })
  }

  /** Uninstalls an extension by pkgName. Returns the refreshed full list (§16). */
  async function uninstallExtension(id: string): Promise<void> {
    await extMutate(id, async () => {
      const res = await apiClient.DELETE('/api/suwayomi/extensions/{pkgName}', {
        params: { path: { pkgName: id } },
      })
      if (res.error) throw new Error(res.error.message)
      return res.data
    })
  }

  /**
   * Triggers a check-for-updates across installed extensions (POST /refresh).
   * Drives checkingUpdates (the button spinner) rather than extensionAction.busyId
   * since this is a global action, not per-row. Applies the refreshed list (§16);
   * surfaces any failure in extensionAction.error for the pane-level banner.
   */
  async function checkUpdates(): Promise<void> {
    checkingUpdates.value = true
    extensionAction.value = { busyId: null }
    try {
      const res = await apiClient.POST('/api/suwayomi/extensions/refresh')
      if (res.error) throw new Error(res.error.message)
      applyExtensionDTOs(res.data)
    }
    catch (e) {
      extensionAction.value = {
        busyId: null,
        error: e instanceof Error ? e.message : 'Failed to check for updates',
      }
    }
    finally {
      checkingUpdates.value = false
    }
  }

  // ── Repo mutations (whole-list PUT) ─────────────────────────────────────────

  /**
   * Internal helper for all repo mutations. Sets busyId, PUTs the full updated
   * URL list, applies the authoritative response (§16), clears state. Surfaces
   * any backend 400/502 error (e.g. Suwayomi's repo-URL format validation) in
   * repoAction.error.
   */
  async function repoMutate(busyId: string, newUrls: string[]): Promise<void> {
    repoAction.value = { busyId }
    try {
      const res = await apiClient.PUT('/api/suwayomi/extensions/repos', {
        body: { repos: newUrls },
      })
      if (res.error) throw new Error(res.error.message)
      applyRepoUrls(res.data.repos)
      repoAction.value = { busyId: null }
    }
    catch (e) {
      repoAction.value = {
        busyId: null,
        error: e instanceof Error ? e.message : 'Repo update failed',
      }
    }
  }

  /**
   * Appends a new repo URL. Guards with isHttpUrl (mirrors the backend
   * pkg/urlx.IsAbsoluteHTTP rule) before the PUT — the server validates regardless.
   */
  async function addRepo(url: string): Promise<void> {
    if (!isHttpUrl(url)) {
      repoAction.value = { busyId: null, error: 'Enter a valid http(s) URL' }
      return
    }
    const currentUrls = repos.value.map(r => r.url)
    await repoMutate(ADD_ACTION_ID, [...currentUrls, url])
  }

  /** Removes a repo by id (id === url for all repos). */
  async function removeRepo(id: string): Promise<void> {
    const newUrls = repos.value.map(r => r.url).filter(u => u !== id)
    await repoMutate(id, newUrls)
  }

  /**
   * Swaps the repo at the given id with its neighbor in the given direction
   * (−1 = up, +1 = down). No-ops silently when already at the list edge.
   */
  async function reorderRepo({ id, direction }: { id: string, direction: ReorderDirection }): Promise<void> {
    const urls = repos.value.map(r => r.url)
    const idx = urls.findIndex(u => u === id)
    if (idx === -1) return
    const newIdx = idx + direction
    if (newIdx < 0 || newIdx >= urls.length) return

    const newUrls = [...urls]
    ;[newUrls[idx], newUrls[newIdx]] = [newUrls[newIdx], newUrls[idx]]

    await repoMutate(id, newUrls)
  }

  void refresh()

  return {
    extensions,
    availableExtensions,
    repos,
    extensionAction,
    repoAction,
    extCheckInterval,
    checkingUpdates,
    pending,
    error,
    installExtension,
    updateExtension,
    uninstallExtension,
    checkUpdates,
    addRepo,
    removeRepo,
    reorderRepo,
    refresh,
  }
}
