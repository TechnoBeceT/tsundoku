/**
 * Settings page route integration. The page, router, screen, source panel, and
 * source-effective-configuration composable are real; only the generated API
 * boundary is replaced with complete deterministic responses.
 *
 * Regressions caught here:
 *   - coercing a source id before the detail request;
 *   - reading the route only once instead of on reload/history transitions;
 *   - writing back from the route watcher and creating a feedback loop;
 *   - letting an exception-summary failure blank global controls or masquerade
 *     as an empty successful exception list.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import type { Router } from 'vue-router'
import { NuxtPage } from '#components'
import Settings from '~/components/screens/Settings.vue'
import type { NetworkEndpointInput } from '~/components/screens/settings.types'
import {
  comicAsuraSourceConfiguration,
  flareSolverrConfig,
  fullyInheritedSourceConfiguration,
  impersonateConfig,
  librarySettings,
  sourcesSettings,
} from '~/fixtures/settings'
import type { components } from '~/utils/api/schema.d.ts'

type SourceConfiguration = components['schemas']['SourceEffectiveConfiguration']
interface DetailResult {
  data?: SourceConfiguration
  error?: { message: string }
  response: Response
}

const apiState = vi.hoisted(() => ({
  detailById: new Map<string, SourceConfiguration>(),
  detailSourceIds: [] as string[],
  nextDetailResponse: null as Promise<DetailResult> | null,
  summaryAttempts: 0,
  summaryFailuresRemaining: 0,
  summaries: [] as unknown[],
  sources: [] as unknown[],
  catalogAttempts: 0,
  catalogFailuresRemaining: 0,
  endpointSaveFailuresRemaining: 0,
}))

vi.mock('~/utils/api/client', () => {
  const ok = (data: unknown) => Promise.resolve({ data, error: null, response: new Response() })
  const GET = vi.fn((path: string, options?: { params?: { path?: { sourceId?: string } } }) => {
    if (path === '/api/owner/me') return ok({ authenticated: true, ownerId: 'owner' })
    if (path === '/api/settings') return ok([])
    if (path === '/api/system') return ok({ storageFolder: '/library', serverPort: '3000', database: 'postgres' })
    if (path === '/api/categories') return ok([])
    if (path === '/api/flaresolverr/settings') {
      return ok({ enabled: false, url: '', timeout: 60, sessionName: '', sessionTtl: 15, asResponseFallback: false })
    }
    if (path === '/api/impersonate') return ok({ enabled: false, url: '', sourceIds: [] })
    if (path === '/api/sources') {
      apiState.catalogAttempts += 1
      if (apiState.catalogFailuresRemaining > 0) {
        apiState.catalogFailuresRemaining -= 1
        return Promise.resolve({
          data: undefined,
          error: { message: 'Source catalog could not be loaded.' },
          response: new Response(null, { status: 503 }),
        })
      }
      return ok(apiState.sources)
    }
    if (path === '/api/suwayomi/extensions') return ok([])
    if (path === '/api/suwayomi/extensions/repos') return ok({ repos: [] })
    if (path === '/api/trackers') return ok([])
    if (path === '/api/network/endpoints') return ok([])
    if (path === '/api/sources/exceptions') {
      apiState.summaryAttempts += 1
      if (apiState.summaryFailuresRemaining > 0) {
        apiState.summaryFailuresRemaining -= 1
        return Promise.resolve({
          data: undefined,
          error: { message: 'Source exceptions could not be loaded.' },
          response: new Response(null, { status: 503 }),
        })
      }
      return ok(apiState.summaries)
    }
    if (path === '/api/sources/{sourceId}/effective-configuration') {
      const sourceId = options?.params?.path?.sourceId ?? ''
      apiState.detailSourceIds.push(sourceId)
      if (apiState.nextDetailResponse) {
        const response = apiState.nextDetailResponse
        apiState.nextDetailResponse = null
        return response
      }
      return ok(apiState.detailById.get(sourceId))
    }
    return ok([])
  })

  const PATCH = vi.fn((path: string, options?: { params?: { path?: { sourceId?: string } }, body?: Record<string, unknown> }) => {
    if (path === '/api/settings') {
      const settings = (options?.body as { settings?: unknown[] } | undefined)?.settings ?? []
      return ok(settings)
    }
    if (path === '/api/flaresolverr/settings') return ok(options?.body)
    if (path === '/api/network/endpoints/{id}') {
      if (apiState.endpointSaveFailuresRemaining > 0) {
        apiState.endpointSaveFailuresRemaining -= 1
        return Promise.resolve({
          data: undefined,
          error: { message: 'Endpoint could not be saved.' },
          response: new Response(null, { status: 503 }),
        })
      }
      return ok(options?.body)
    }
    if (path === '/api/sources/{sourceId}/throughput') {
      return ok({
        sourceId: options?.params?.path?.sourceId ?? '',
        downloadConcurrency: { override: 2, effective: 2 },
        imageRequestDelay: { override: null, effective: '500ms' },
      })
    }
    return ok(null)
  })
  const PUT = vi.fn((path: string, options?: { body?: Record<string, unknown> }) => {
    if (path === '/api/impersonate') {
      return ok({ ...options?.body, sourceIds: impersonateConfig.sourceIds })
    }
    return ok(null)
  })

  return {
    apiClient: {
      GET,
      POST: vi.fn(() => ok(null)),
      PUT,
      PATCH,
      DELETE: vi.fn(() => ok(null)),
      use: vi.fn(),
    },
    setUnauthorizedHandler: vi.fn(),
  }
})

type MountedPage = Awaited<ReturnType<typeof mountPage>>
interface RoutedVm { $router: Router }

function sourceOption(configuration: SourceConfiguration) {
  return {
    id: configuration.source.sourceId,
    name: configuration.source.name,
    lang: configuration.source.language,
  }
}

function summary(configuration: SourceConfiguration) {
  return {
    source: configuration.source,
    exceptionCount: 2,
    runtime: configuration.runtime,
  }
}

function endpointInput(overrides: Partial<NetworkEndpointInput> = {}): NetworkEndpointInput {
  return {
    id: 'ep-flare',
    name: 'VPN FlareSolverr',
    kind: 'flaresolverr',
    enabled: true,
    host: '',
    port: 0,
    socksVersion: 5,
    username: '',
    password: '',
    url: 'http://flare:8191',
    session: 'source-session',
    sessionTtl: 15,
    timeout: 60,
    asResponseFallback: false,
    ...overrides,
  }
}

async function mountPage(route: string) {
  const wrapper = await mountSuspended(NuxtPage, {
    route,
    attachTo: document.body,
    global: { stubs: { Icon: true } },
  })
  await flushPromises()
  await wrapper.vm.$nextTick()
  return wrapper
}

async function waitForRoute(wrapper: MountedPage, sourceId: string): Promise<void> {
  await vi.waitFor(() => {
    const configuration = apiState.detailById.get(sourceId)!
    expect((wrapper.vm as unknown as RoutedVm).$router.currentRoute.value.query.source).toBe(sourceId)
    expect(wrapper.get('[data-testid="source-editor"]').text()).toContain(configuration.source.name)
  })
  await flushPromises()
}

beforeEach(() => {
  apiState.detailById = new Map<string, SourceConfiguration>([
    [fullyInheritedSourceConfiguration.source.sourceId, fullyInheritedSourceConfiguration],
    [comicAsuraSourceConfiguration.source.sourceId, comicAsuraSourceConfiguration],
  ])
  apiState.detailSourceIds = []
  apiState.nextDetailResponse = null
  apiState.summaryAttempts = 0
  apiState.summaryFailuresRemaining = 0
  apiState.summaries = [summary(fullyInheritedSourceConfiguration), summary(comicAsuraSourceConfiguration)]
  apiState.sources = [sourceOption(fullyInheritedSourceConfiguration), sourceOption(comicAsuraSourceConfiguration)]
  apiState.catalogAttempts = 0
  apiState.catalogFailuresRemaining = 0
  apiState.endpointSaveFailuresRemaining = 0
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
    configurable: true,
    value: vi.fn(),
  })
})

describe('Settings page route integration', () => {
  it('loads a deep-linked lossless source id and restores its row focus on remount', async () => {
    const sourceId = fullyInheritedSourceConfiguration.source.sourceId
    const route = `/settings?pane=download-engine&source=${sourceId}&setting=imageRequestDelay`

    const first = await mountPage(route)
    expect(apiState.detailSourceIds).toEqual([sourceId])
    expect(document.activeElement).toBe(first.get('[data-source-setting-target="imageRequestDelay"]').element)
    first.unmount()

    const reloaded = await mountPage(route)
    expect(apiState.detailSourceIds).toEqual([sourceId, sourceId])
    expect(document.activeElement).toBe(reloaded.get('[data-source-setting-target="imageRequestDelay"]').element)
    reloaded.unmount()
  })

  it('tracks push, back, and forward without watcher write-back or duplicate source loads', async () => {
    const firstId = fullyInheritedSourceConfiguration.source.sourceId
    const secondId = comicAsuraSourceConfiguration.source.sourceId
    const wrapper = await mountPage(`/settings?pane=download-engine&source=${firstId}&setting=imageRequestDelay`)
    const { $router: router } = wrapper.vm as unknown as RoutedVm
    const push = vi.spyOn(router, 'push')

    await router.push({
      path: '/settings',
      query: { pane: 'download-engine', source: secondId, setting: 'byparr' },
    })
    await waitForRoute(wrapper, secondId)
    expect(document.activeElement).toBe(wrapper.get('[data-source-setting-target="byparr"]').element)
    expect(push).toHaveBeenCalledTimes(1)
    expect(apiState.detailSourceIds).toEqual([firstId, secondId])

    router.back()
    await waitForRoute(wrapper, firstId)
    expect(document.activeElement).toBe(wrapper.get('[data-source-setting-target="imageRequestDelay"]').element)
    expect(push).toHaveBeenCalledTimes(1)
    expect(apiState.detailSourceIds).toEqual([firstId, secondId, firstId])

    router.forward()
    await waitForRoute(wrapper, secondId)
    expect(document.activeElement).toBe(wrapper.get('[data-source-setting-target="byparr"]').element)
    expect(push).toHaveBeenCalledTimes(1)
    expect(apiState.detailSourceIds).toEqual([firstId, secondId, firstId, secondId])
    wrapper.unmount()
  })

  it('retries a local summary failure without blanking global download controls', async () => {
    apiState.summaryFailuresRemaining = 1
    const wrapper = await mountPage('/settings?pane=download-engine')

    expect(wrapper.text()).toContain('Scheduling')
    expect(wrapper.text()).toContain('Source exceptions could not be loaded.')
    expect(wrapper.text()).not.toContain('Every source currently inherits the global settings.')

    await wrapper.get('[data-testid="retry-source-summaries"]').trigger('click')
    await flushPromises()

    expect(apiState.summaryAttempts).toBe(2)
    expect(wrapper.text()).toContain('Scheduling')
    expect(wrapper.text()).not.toContain('Source exceptions could not be loaded.')
    wrapper.unmount()
  })

  it('refreshes the selected effective configuration and summaries after every relevant global save', async () => {
    const sourceId = fullyInheritedSourceConfiguration.source.sourceId
    const initial = {
      ...fullyInheritedSourceConfiguration,
      bypassEnabled: false,
      imageProxy: { ...fullyInheritedSourceConfiguration.imageProxy, gatewayEnabled: false, effectiveAvailable: false },
    } satisfies SourceConfiguration
    apiState.detailById.set(sourceId, initial)
    const wrapper = await mountPage(`/settings?pane=download-engine&source=${sourceId}`)
    const screen = wrapper.getComponent(Settings)

    const scheduling = {
      ...initial,
      downloadConcurrency: { override: null, effective: 9, inherited: true },
    } satisfies SourceConfiguration
    apiState.detailById.set(sourceId, scheduling)
    screen.vm.$emit('save-library', { ...librarySettings, downloadConcurrency: 9 })
    await vi.waitFor(() => expect(apiState.detailSourceIds).toHaveLength(2))
    expect(wrapper.get('[data-source-setting-target="downloadConcurrency"]').text()).toContain('Global 9')
    expect(wrapper.get('[data-source-setting-target="downloadConcurrency"]').text()).toContain('Effective 9')

    const protection = {
      ...scheduling,
      imageRequestDelay: { override: null, effective: '900ms', inherited: true },
      protection: { ...scheduling.protection, failureThreshold: 8 },
    } satisfies SourceConfiguration
    apiState.detailById.set(sourceId, protection)
    screen.vm.$emit('save-sources-settings', {
      ...sourcesSettings,
      failureThreshold: 8,
      imageRequestDelayMs: 900,
    })
    await vi.waitFor(() => expect(apiState.detailSourceIds).toHaveLength(3))
    expect(wrapper.get('[data-source-setting-target="imageRequestDelay"]').text()).toContain('Global 900ms')
    expect(wrapper.get('[data-source-setting-target="imageRequestDelay"]').text()).toContain('Effective 900ms')
    expect(wrapper.get('[data-testid="source-editor"]').text()).toContain('Failure threshold8')

    const bypass = { ...protection, bypassEnabled: true } satisfies SourceConfiguration
    apiState.detailById.set(sourceId, bypass)
    screen.vm.$emit('save-flaresolverr', { ...flareSolverrConfig, enabled: true, url: 'http://flare:8191' })
    await vi.waitFor(() => expect(apiState.detailSourceIds).toHaveLength(4))
    expect(wrapper.get('[data-source-setting-target="byparr"]').text()).toContain('Enabled')

    const gateway = {
      ...bypass,
      imageProxy: { ...bypass.imageProxy, gatewayEnabled: true, gatewayConfigured: true },
    } satisfies SourceConfiguration
    apiState.detailById.set(sourceId, gateway)
    screen.vm.$emit('save-impersonate', { enabled: true, url: 'http://impersonate:8788' })
    await vi.waitFor(() => expect(apiState.detailSourceIds).toHaveLength(5))
    expect(wrapper.get('[data-testid="source-editor"]').text()).toContain('Gateway enabledYes')
    expect(apiState.summaryAttempts).toBe(5)
    wrapper.unmount()
  })

  it('refreshes the selected effective configuration and summaries after an endpoint edit', async () => {
    const sourceId = fullyInheritedSourceConfiguration.source.sourceId
    const wrapper = await mountPage(`/settings?pane=download-engine&source=${sourceId}`)
    const screen = wrapper.getComponent(Settings)
    apiState.detailById.set(sourceId, {
      ...fullyInheritedSourceConfiguration,
      bypassEnabled: false,
      profileKey: 'endpoint-revision-2',
    })

    screen.vm.$emit('save-endpoint', endpointInput({ enabled: false }))

    await vi.waitFor(() => expect(apiState.detailSourceIds).toHaveLength(2))
    expect(wrapper.get('[data-source-setting-target="byparr"]').text()).toContain('Disabled')
    expect(apiState.summaryAttempts).toBe(2)
    wrapper.unmount()
  })

  it('does not refresh source projections after a rejected endpoint edit', async () => {
    const sourceId = fullyInheritedSourceConfiguration.source.sourceId
    const wrapper = await mountPage(`/settings?pane=download-engine&source=${sourceId}`)
    const screen = wrapper.getComponent(Settings)
    apiState.endpointSaveFailuresRemaining = 1

    screen.vm.$emit('save-endpoint', endpointInput({ enabled: false }))

    await vi.waitFor(() => expect(wrapper.text()).toContain('Endpoint could not be saved.'))
    expect(apiState.detailSourceIds).toEqual([sourceId])
    expect(apiState.summaryAttempts).toBe(1)
    expect(wrapper.get('[data-source-setting-target="byparr"]').text()).toContain('Enabled')
    wrapper.unmount()
  })

  it('keeps the confirmed editor visible while a row mutation awaits its confirmation GET', async () => {
    const sourceId = fullyInheritedSourceConfiguration.source.sourceId
    const wrapper = await mountPage(`/settings?pane=download-engine&source=${sourceId}`)
    let confirm!: (result: DetailResult) => void
    apiState.nextDetailResponse = new Promise(resolve => { confirm = resolve })
    const row = wrapper.get('[data-source-setting-target="downloadConcurrency"]')

    await row.get('input[type="number"]').setValue('2')
    await row.get('[data-testid="set-override"]').trigger('click')
    await vi.waitFor(() => expect(apiState.nextDetailResponse).toBeNull())

    expect(wrapper.find('[data-testid="source-editor"]').exists()).toBe(true)
    expect(wrapper.get('[data-source-setting-target="downloadConcurrency"]').attributes('aria-busy')).toBe('true')
    expect(wrapper.get('[data-source-setting-target="downloadConcurrency"]').text()).toContain('Effective 5')

    const confirmed = {
      ...fullyInheritedSourceConfiguration,
      downloadConcurrency: { override: 2, effective: 2, inherited: false },
    } satisfies SourceConfiguration
    confirm({ data: confirmed, response: new Response() })
    await vi.waitFor(() => {
      expect(wrapper.get('[data-source-setting-target="downloadConcurrency"]').text()).toContain('Effective 2')
    })
    wrapper.unmount()
  })

  it('keeps the confirmed editor visible and reports a rejected confirmation on its row', async () => {
    const sourceId = fullyInheritedSourceConfiguration.source.sourceId
    const wrapper = await mountPage(`/settings?pane=download-engine&source=${sourceId}`)
    let rejectConfirmation!: (cause: Error) => void
    apiState.nextDetailResponse = new Promise((_, reject) => { rejectConfirmation = reject })
    const row = wrapper.get('[data-source-setting-target="downloadConcurrency"]')

    await row.get('input[type="number"]').setValue('2')
    await row.get('[data-testid="set-override"]').trigger('click')
    await vi.waitFor(() => expect(apiState.nextDetailResponse).toBeNull())
    rejectConfirmation(new Error('Confirmation read failed'))

    await vi.waitFor(() => {
      expect(wrapper.get('[data-source-setting-target="downloadConcurrency"]').text()).toContain('Confirmation read failed')
    })
    expect(wrapper.find('[data-testid="source-editor"]').exists()).toBe(true)
    expect(wrapper.get('[data-source-setting-target="downloadConcurrency"]').text()).toContain('Effective 5')
    expect(wrapper.find('[aria-label="Loading source configuration"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows and retries an initial source-catalog failure without claiming the catalog is empty', async () => {
    apiState.catalogFailuresRemaining = 1
    const wrapper = await mountPage('/settings?pane=download-engine')

    expect(wrapper.text()).toContain('Source catalog could not be loaded.')
    expect(wrapper.text()).not.toContain('No sources installed')
    expect(apiState.catalogAttempts).toBe(1)

    await wrapper.get('[data-testid="retry-source-catalog"]').trigger('click')
    await vi.waitFor(() => expect(apiState.catalogAttempts).toBe(2))
    expect(wrapper.text()).not.toContain('Source catalog could not be loaded.')
    expect(wrapper.find('input[aria-label="Search installed sources"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
