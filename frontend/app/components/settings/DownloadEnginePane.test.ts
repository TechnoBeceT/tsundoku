/**
 * Download engine composition contracts.
 *
 * Mutations caught here:
 *   - duplicating a global control while consolidating old panes;
 *   - reviving either whole-catalog throughput or proxy-membership forms;
 *   - sending a contextual shortcut anywhere except the canonical editor;
 *   - dropping endpoint CRUD or moving maintenance into source exceptions;
 *   - rendering more than one source-configuration editor.
 */
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import DownloadEnginePane from './DownloadEnginePane.vue'
import LibraryDedupDialog from './LibraryDedupDialog.vue'
import NetworkEndpointRow from './NetworkEndpointRow.vue'
import RedownloadDialog from './RedownloadDialog.vue'
import SourceBindingRow from './SourceBindingRow.vue'
import SourceExceptionsPanel from './SourceExceptionsPanel.vue'
import SourceThroughputControl from './SourceThroughputControl.vue'
import SourcesSettingsPane from './SourcesSettingsPane.vue'
import {
  comicAsuraSourceConfiguration,
  flareSolverrConfig,
  fullyInheritedSourceConfiguration,
  hiveProxySourceConfiguration,
  impersonateConfig,
  librarySettings,
  networkEndpoints,
  sourcesSettings,
} from '../../fixtures/settings'
import type { components } from '../../utils/api/schema.d.ts'

type SourceExceptionSummary = components['schemas']['SourceExceptionSummary']

const sourceSummaries = [
  {
    source: comicAsuraSourceConfiguration.source,
    exceptionCount: 6,
    runtime: comicAsuraSourceConfiguration.runtime,
  },
  {
    source: hiveProxySourceConfiguration.source,
    exceptionCount: 1,
    runtime: hiveProxySourceConfiguration.runtime,
  },
] satisfies SourceExceptionSummary[]

const baseProps = {
  library: librarySettings,
  sources: sourcesSettings,
  flareSolverr: flareSolverrConfig,
  impersonate: impersonateConfig,
  endpoints: networkEndpoints,
  sourceCatalog: [
    fullyInheritedSourceConfiguration.source,
    comicAsuraSourceConfiguration.source,
    hiveProxySourceConfiguration.source,
  ],
  sourceSummaries,
  selectedSourceId: comicAsuraSourceConfiguration.source.sourceId,
  sourceConfiguration: comicAsuraSourceConfiguration,
}

const mountPane = () => mount(DownloadEnginePane, { props: baseProps })

describe('DownloadEnginePane', () => {
  it('renders exactly five canonical anchored sections in the intended hierarchy', () => {
    const wrapper = mountPane()
    const sections = wrapper.findAll('[data-engine-section]')

    expect(sections.map(section => section.attributes('id'))).toEqual([
      'download-engine-scheduling',
      'download-engine-protection',
      'download-engine-access',
      'download-engine-routing',
      'download-engine-source-exceptions',
    ])
    expect(sections.map(section => section.get('h2').text())).toEqual([
      'Scheduling',
      'Protection',
      'Access & bypass',
      'Routing',
      'Source exceptions',
    ])
    expect(wrapper.findAll('[data-testid="engine-section-nav"] a')).toHaveLength(5)
  })

  it('uses one h1, canonical h2 section titles, and nested h3 card titles', () => {
    const wrapper = mountPane()

    expect(wrapper.findAll('h1').map(heading => heading.text())).toEqual([
      'Defaults first. Exceptions only where needed.',
    ])
    expect(wrapper.findAll('h2').map(heading => heading.text())).toEqual([
      'Scheduling',
      'Protection',
      'Access & bypass',
      'Routing',
      'Source exceptions',
    ])
    expect(wrapper.findAll('h3').map(heading => heading.text())).toEqual(expect.arrayContaining([
      'Cadence & capacity',
      'Anti-block protection',
      'Cloudflare bypass (FlareSolverr)',
      'Chrome-fingerprint image proxy',
      'Egress endpoints',
      'Source exceptions',
    ]))
  })

  it('shows every global control once and removes the old all-source forms', () => {
    const wrapper = mountPane()
    const globalSections = [
      wrapper.get('#download-engine-scheduling').text(),
      wrapper.get('#download-engine-protection').text(),
      wrapper.get('#download-engine-access').text(),
    ].join(' ')

    for (const label of [
      'Refresh interval',
      'Download interval',
      'Chapter retry backoff',
      'Chapter max retries',
      'Warm-up interval',
      'Failure threshold',
      'Politeness delay',
      'Image request delay',
      'Cloudflare bypass (FlareSolverr)',
      'Chrome-fingerprint image proxy',
    ]) {
      expect(globalSections.split(label)).toHaveLength(2)
    }

    expect(wrapper.findComponent(SourceThroughputControl).exists()).toBe(false)
    expect(wrapper.findAll('[aria-label^="Use the image proxy for "]')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('Per-source download pace')
    expect(wrapper.text()).not.toContain('Sources using the proxy')
  })

  it('targets every contextual shortcut at the one canonical Source exceptions panel', () => {
    const wrapper = mountPane()
    const shortcuts = wrapper.findAll('a[data-source-exceptions-shortcut]')

    expect(shortcuts.length).toBeGreaterThan(1)
    expect(shortcuts.every(link => link.attributes('href') === '#download-engine-source-exceptions')).toBe(true)
    expect(wrapper.findAllComponents(SourceExceptionsPanel)).toHaveLength(1)
    expect(wrapper.findAll('[data-testid="source-editor"]')).toHaveLength(1)
  })

  it('keeps endpoint CRUD in Routing while per-source route membership stays in Source exceptions', async () => {
    const wrapper = mountPane()
    const routing = wrapper.get('#download-engine-routing')

    expect(routing.findAllComponents(NetworkEndpointRow)).toHaveLength(networkEndpoints.length)
    expect(routing.get('button').text()).toContain('Add endpoint')
    expect(routing.findComponent(SourceBindingRow).exists()).toBe(false)

    await routing.get('button').trigger('click')
    expect(document.body.querySelector('[role="dialog"]')).not.toBeNull()
    wrapper.unmount()
  })

  it('keeps library maintenance and bulk actions outside the composed pane', () => {
    const wrapper = mountPane()
    expect(wrapper.findComponent(LibraryDedupDialog).exists()).toBe(false)
    expect(wrapper.findComponent(RedownloadDialog).exists()).toBe(false)

    const maintenance = mount(SourcesSettingsPane, { props: { sources: sourcesSettings } })
    expect(maintenance.findComponent(LibraryDedupDialog).exists()).toBe(true)
    expect(maintenance.findComponent(RedownloadDialog).exists()).toBe(true)
  })

  it('forwards canonical source selection and row mutations without changing string ids', () => {
    const wrapper = mountPane()
    const panel = wrapper.getComponent(SourceExceptionsPanel)

    panel.vm.$emit('select-source', '9127482910938471028')
    panel.vm.$emit('set-override', '9127482910938471028', 'downloadConcurrency', 1)

    expect(wrapper.emitted('select-source')?.[0]).toEqual(['9127482910938471028'])
    expect(wrapper.emitted('set-source-override')?.[0]).toEqual([
      '9127482910938471028',
      'downloadConcurrency',
      1,
    ])
  })
})
