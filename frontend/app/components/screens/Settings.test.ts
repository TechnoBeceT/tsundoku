/** Settings navigation, consolidated pane ownership, and contextual focus. */
import { afterEach, describe, it, expect, vi } from 'vitest'
import { nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import Settings from './Settings.vue'
import DownloadEnginePane from '../settings/DownloadEnginePane.vue'
import EnginePane from '../settings/EnginePane.vue'
import LibraryPane from '../settings/LibraryPane.vue'
import SourcesSettingsPane from '../settings/SourcesSettingsPane.vue'
import SourceMetricsPane from '../health/SourceMetricsPane.vue'
import {
  availableExtensions,
  comicAsuraSourceConfiguration,
  engineInfo,
  extCheckInterval,
  flareSolverrConfig,
  fullyInheritedSourceConfiguration,
  impersonateConfig,
  installedExtensions,
  librarySettings,
  networkEndpoints,
  pendingSourceException,
  repos,
  settingsCategories,
  sourcesSettings,
  systemInfo,
} from '../../fixtures/settings'

vi.mock('~/utils/api/client', () => ({
  apiClient: { GET: vi.fn().mockResolvedValue({ data: { authenticated: true, ownerId: 'owner' } }) },
  setUnauthorizedHandler: vi.fn(),
}))

const baseProps = {
  library: librarySettings,
  system: systemInfo,
  categories: settingsCategories,
  engine: engineInfo,
  flareSolverr: flareSolverrConfig,
  impersonate: impersonateConfig,
  extensions: installedExtensions,
  availableExtensions,
  repos,
  extCheckInterval,
  sourcesSettings,
  networkEndpoints,
  sourceCatalog: [
    fullyInheritedSourceConfiguration.source,
    comicAsuraSourceConfiguration.source,
  ],
  sourceSummaries: [pendingSourceException],
  selectedSourceId: comicAsuraSourceConfiguration.source.sourceId,
  sourceConfiguration: comicAsuraSourceConfiguration,
}

function mountScreen(props: Record<string, unknown> = {}) {
  return mount(Settings, {
    attachTo: document.body,
    props: { ...baseProps, ...props },
  })
}

afterEach(() => {
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

describe('Settings navigation ownership', () => {
  it('replaces Server config, Sources, and Network with one Download engine destination', () => {
    const wrapper = mountScreen({ activePane: 'library' })
    const labels = wrapper.findAll('nav button').map(button => button.text())

    expect(labels.filter(label => label === 'Download engine')).toHaveLength(1)
    expect(labels).not.toContain('Server config')
    expect(labels).not.toContain('Sources')
    expect(labels).not.toContain('Network')
    expect(labels).toContain('Engine diagnostics')
  })

  it('keeps library maintenance with Library and engine lifecycle in Engine diagnostics', async () => {
    const wrapper = mountScreen({ activePane: 'library' })

    expect(wrapper.findComponent(LibraryPane).exists()).toBe(true)
    expect(wrapper.findComponent(SourcesSettingsPane).exists()).toBe(true)
    expect(wrapper.findComponent(SourceMetricsPane).exists()).toBe(false)

    await wrapper.setProps({ activePane: 'engine' })
    expect(wrapper.findComponent(EnginePane).exists()).toBe(true)
    expect(wrapper.findComponent(DownloadEnginePane).exists()).toBe(false)
  })
})

describe('Settings contextual source focus', () => {
  it('selects the linked source and focuses/highlights its canonical row', async () => {
    const wrapper = mountScreen({
      activePane: 'download-engine',
      highlightedSourceId: comicAsuraSourceConfiguration.source.sourceId,
      highlightedSetting: 'imageRequestDelay',
    })

    await nextTick()
    const target = wrapper.get('[data-source-setting-target="imageRequestDelay"]')

    expect(wrapper.get('[data-testid="source-editor"]').text()).toContain('Comic Asura')
    expect(target.attributes('data-highlighted-setting')).toBe('true')
    expect(document.activeElement).toBe(target.element)
  })

  it('restores the same selected source and row focus after a remount', async () => {
    const props = {
      activePane: 'download-engine',
      highlightedSourceId: comicAsuraSourceConfiguration.source.sourceId,
      highlightedSetting: 'downloadConcurrency',
    }
    const first = mountScreen(props)
    await nextTick()
    expect(document.activeElement).toBe(first.get('[data-source-setting-target="downloadConcurrency"]').element)
    first.unmount()

    const reloaded = mountScreen(props)
    await nextTick()
    expect(reloaded.get('[data-testid="source-editor"]').text()).toContain('Comic Asura')
    expect(document.activeElement).toBe(reloaded.get('[data-source-setting-target="downloadConcurrency"]').element)
  })

  it('maps effective bypass and both routing keys to their canonical visible rows', async () => {
    const wrapper = mountScreen({
      activePane: 'download-engine',
      highlightedSourceId: comicAsuraSourceConfiguration.source.sourceId,
      highlightedSetting: 'byparr',
    })

    await nextTick()
    expect(document.activeElement).toBe(wrapper.get('[data-source-setting-target="byparr"]').element)

    await wrapper.setProps({ highlightedSetting: 'socksBinding' })
    await nextTick()
    expect(document.activeElement).toBe(wrapper.get('[data-source-setting-target="routing"]').element)

    await wrapper.setProps({ highlightedSetting: 'bypassBinding' })
    await nextTick()
    expect(document.activeElement).toBe(wrapper.get('[data-source-setting-target="routing"]').element)
  })

  it('contains source-detail failure inside Download engine without blanking global or Library settings', async () => {
    const wrapper = mountScreen({
      activePane: 'library',
      sourceConfiguration: null,
      sourceConfigurationError: 'This source could not be loaded.',
    })

    expect(wrapper.text()).toContain('Library behavior')
    expect(wrapper.text()).toContain('Library maintenance')
    expect(wrapper.text()).not.toContain('This source could not be loaded.')

    await wrapper.setProps({ activePane: 'download-engine' })
    expect(wrapper.text()).toContain('Scheduling')
    expect(wrapper.text()).toContain('This source could not be loaded.')
  })
})
