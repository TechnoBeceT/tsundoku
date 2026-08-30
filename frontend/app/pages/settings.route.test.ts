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
import {
  comicAsuraSourceConfiguration,
  fullyInheritedSourceConfiguration,
} from '~/fixtures/settings'
import type { components } from '~/utils/api/schema.d.ts'

type SourceConfiguration = components['schemas']['SourceEffectiveConfiguration']

const apiState = vi.hoisted(() => ({
  detailById: new Map<string, SourceConfiguration>(),
  detailSourceIds: [] as string[],
  summaryAttempts: 0,
  summaryFailuresRemaining: 0,
  summaries: [] as unknown[],
  sources: [] as unknown[],
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
    if (path === '/api/sources') return ok(apiState.sources)
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
      return ok(apiState.detailById.get(sourceId))
    }
    return ok([])
  })

  return {
    apiClient: {
      GET,
      POST: vi.fn(() => ok(null)),
      PUT: vi.fn(() => ok(null)),
      PATCH: vi.fn(() => ok(null)),
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
  apiState.summaryAttempts = 0
  apiState.summaryFailuresRemaining = 0
  apiState.summaries = [summary(fullyInheritedSourceConfiguration), summary(comicAsuraSourceConfiguration)]
  apiState.sources = [sourceOption(fullyInheritedSourceConfiguration), sourceOption(comicAsuraSourceConfiguration)]
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
})
