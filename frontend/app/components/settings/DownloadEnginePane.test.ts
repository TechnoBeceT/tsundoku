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

type PaneProps = InstanceType<typeof DownloadEnginePane>['$props']

const mountPane = (overrides: Partial<PaneProps> = {}) => mount(DownloadEnginePane, {
  attachTo: document.body,
  props: { ...baseProps, ...overrides } as PaneProps,
})

describe('DownloadEnginePane', () => {
  it('renders exactly five linked tabs and five stable panels with Scheduling active by default', () => {
    const wrapper = mountPane({ selectedSourceId: null, sourceConfiguration: null })
    const tabs = wrapper.findAll('[role="tab"]')
    const sections = wrapper.findAll('[role="tabpanel"]')

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
    expect(tabs.map(tab => tab.text())).toEqual([
      'Scheduling',
      'Protection',
      'Access & bypass',
      'Routing',
      'Source exceptions',
    ])
    expect(tabs.map(tab => tab.attributes('aria-controls'))).toEqual(sections.map(section => section.attributes('id')))
    expect(tabs.map(tab => tab.attributes('aria-selected'))).toEqual(['true', 'false', 'false', 'false', 'false'])
    expect(sections[0]!.attributes('hidden')).toBeUndefined()
    expect(sections.slice(1).every(section => section.attributes('hidden') !== undefined)).toBe(true)
    expect(wrapper.findAllComponents(SourceExceptionsPanel)).toHaveLength(1)
    wrapper.unmount()
  })

  it('keeps edited scheduling state and save boundaries intact across tab switches', async () => {
    const wrapper = mountPane({ selectedSourceId: null, sourceConfiguration: null })
    const maxRetries = wrapper.get<HTMLInputElement>('input[aria-label="Chapter max retries"]')

    await maxRetries.setValue('12')
    await wrapper.findAll('[role="tab"]')[1]!.trigger('click')
    await wrapper.findAll('[role="tab"]')[0]!.trigger('click')
    expect(wrapper.get<HTMLInputElement>('input[aria-label="Chapter max retries"]').element.value).toBe('12')

    await wrapper.get('#download-engine-scheduling .save-foot button').trigger('click')
    expect(wrapper.emitted('save-library')?.[0]?.[0]).toMatchObject({ maxRetries: 12 })
    wrapper.unmount()
  })

  it('keeps endpoint dialog and error state mounted across tab switches', async () => {
    const wrapper = mountPane({
      selectedSourceId: null,
      sourceConfiguration: null,
      endpointAction: { busyId: null, error: 'Endpoint could not be saved.' },
    })

    await wrapper.findAll('[role="tab"]')[3]!.trigger('click')
    await wrapper.get('#download-engine-routing button').trigger('click')
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('Endpoint could not be saved.')

    await wrapper.findAll('[role="tab"]')[0]!.trigger('click')
    await wrapper.findAll('[role="tab"]')[3]!.trigger('click')
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('Endpoint could not be saved.')
    wrapper.unmount()
  })

  it('keeps Source exceptions loading and error state mounted across tab switches', async () => {
    const wrapper = mountPane({
      selectedSourceId: null,
      sourceConfiguration: null,
      sourceExceptionsPending: true,
      sourceSummariesError: 'Source exceptions could not be loaded.',
    })
    const panel = wrapper.get<HTMLElement>('#download-engine-source-exceptions .source-exceptions').element

    await wrapper.findAll('[role="tab"]')[4]!.trigger('click')
    expect(wrapper.get('#download-engine-source-exceptions').text()).toContain('Source exceptions could not be loaded.')
    expect(wrapper.find('[aria-label="Loading source exceptions"]').exists()).toBe(true)

    await wrapper.findAll('[role="tab"]')[0]!.trigger('click')
    await wrapper.findAll('[role="tab"]')[4]!.trigger('click')
    expect(wrapper.get<HTMLElement>('#download-engine-source-exceptions .source-exceptions').element).toBe(panel)
    expect(wrapper.get('#download-engine-source-exceptions').text()).toContain('Source exceptions could not be loaded.')
    expect(wrapper.find('[aria-label="Loading source exceptions"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it.each([
    ['selected source', { selectedSourceId: '9127482910938471028' }],
    ['highlighted source', { selectedSourceId: null, highlightedSourceId: '9127482910938471028' }],
    ['highlighted setting', { selectedSourceId: null, highlightedSetting: 'imageRequestDelay' as const }],
  ])('activates Source exceptions for a %s deep link', (_label, overrides) => {
    const wrapper = mountPane(overrides)

    expect(wrapper.findAll('[role="tab"]')[4]!.attributes('aria-selected')).toBe('true')
    expect(wrapper.get('#download-engine-source-exceptions').attributes('hidden')).toBeUndefined()
    wrapper.unmount()
  })

  it('activates and focuses Source exceptions before a contextual shortcut completes', async () => {
    const wrapper = mountPane({ selectedSourceId: null, sourceConfiguration: null })

    await wrapper.get('button[data-source-exceptions-shortcut]').trigger('click')

    expect(wrapper.findAll('[role="tab"]')[4]!.attributes('aria-selected')).toBe('true')
    expect(wrapper.get('#download-engine-source-exceptions').element).toBe(document.activeElement)
    wrapper.unmount()
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

    expect(wrapper.findAll('[aria-label^="Use the image proxy for "]')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('Per-source download pace')
    expect(wrapper.text()).not.toContain('Sources using the proxy')
  })

  it('gives every global numeric and duration control a unique setting-specific accessible name', async () => {
    const wrapper = mountPane()

    await wrapper.get('.advanced__toggle').trigger('click')

    const globalSections = [
      wrapper.get('#download-engine-scheduling'),
      wrapper.get('#download-engine-protection'),
      wrapper.get('#download-engine-access'),
    ]
    const numberNames = globalSections.flatMap(section => (
      section.findAll('input[type="number"]').map(control => control.attributes('aria-label'))
    ))
    const unitNames = globalSections.flatMap(section => (
      section.findAll('select').map(control => control.attributes('aria-label'))
    ))

    expect(numberNames).toEqual([
      'Refresh interval value',
      'Download interval value',
      'Chapter retry backoff value',
      'Chapter max retries',
      'Stale-grace days',
      'Refresh concurrency',
      'Download concurrency',
      'Max concurrent downloads',
      'Warm-up interval value',
      'Warm-up slow threshold',
      'Failure threshold',
      'Source cooldown value',
      'Politeness delay',
      'Image request delay',
      'FlareSolverr request timeout value',
      'FlareSolverr session TTL value',
    ])
    expect(new Set(numberNames).size).toBe(numberNames.length)
    expect(unitNames).toEqual([
      'Refresh interval unit',
      'Download interval unit',
      'Chapter retry backoff unit',
      'Warm-up interval unit',
      'Source cooldown unit',
      'FlareSolverr request timeout unit',
      'FlareSolverr session TTL unit',
    ])
    expect(new Set(unitNames).size).toBe(unitNames.length)
    const sourceSpecificControl = wrapper.get('[data-source-setting-target="downloadConcurrency"] label')
    expect(sourceSpecificControl.text()).toContain('Chapter concurrency override')
    expect(sourceSpecificControl.find('input[type="number"]').exists()).toBe(true)
  })

  it('targets every contextual shortcut at the one canonical Source exceptions panel', () => {
    const wrapper = mountPane()
    const shortcuts = wrapper.findAll('button[data-source-exceptions-shortcut]')

    expect(shortcuts.length).toBeGreaterThan(1)
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

    const maintenance = mount(SourcesSettingsPane)
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
