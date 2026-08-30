/**
 * Canonical source-exception editor contracts.
 *
 * Mutations caught here:
 *   - searching only the exception endpoint instead of the full source catalog;
 *   - rendering one editor per summary or losing a selected source's effective values;
 *   - obscuring inherited versus overridden settings or proxy membership;
 *   - dropping the source/key from row-local mutations or disabling sibling rows;
 *   - ignoring an externally highlighted source instead of focusing it;
 *   - moving persistence into the presentational component.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import SourceExceptionsPanel from './SourceExceptionsPanel.vue'
import SourceConfigurationGroup from './SourceConfigurationGroup.vue'
import SourceBindingRow from './SourceBindingRow.vue'
import SourceOverrideRow from './SourceOverrideRow.vue'
import SourceProxyOptInRow from './SourceProxyOptInRow.vue'
import type { components } from '../../utils/api/schema.d.ts'
import type { NetworkEndpoint } from '../screens/settings.types'

vi.mock('~/utils/api/client', () => ({
  apiClient: {
    GET: vi.fn().mockResolvedValue({ data: { authenticated: true, ownerId: 'owner' }, error: null, response: new Response() }),
    use: vi.fn(),
  },
  setUnauthorizedHandler: vi.fn(),
}))

type SourceConfiguration = components['schemas']['SourceEffectiveConfiguration']
type SourceException = components['schemas']['SourceExceptionSummary']

const inherited: SourceConfiguration = {
  source: { sourceId: 'source-inherited', name: 'MangaDex', language: 'en' },
  downloadConcurrency: { override: null, effective: 5, inherited: true },
  imageRequestDelay: { override: null, effective: '500ms', inherited: true },
  protection: {
    warmupInterval: '15m0s', warmupSlowThresholdMs: 5000, failureThreshold: 5,
    sourceCooldown: '30m0s', politenessDelay: '500ms',
  },
  bypassEnabled: true,
  reuseBypassSession: { override: null, global: false, effective: false, inherited: true, mode: 'disposable' },
  imageConnectionMode: { override: null, global: 'fresh', effective: 'fresh', inherited: true },
  imageProxy: { optedIn: false, gatewayEnabled: true, gatewayConfigured: true, effectiveAvailable: false },
  routing: {
    socksMode: 'global', socks: { endpointId: null, name: null },
    bypassMode: 'global', bypass: { endpointId: null, name: null },
  },
  profileKey: 'default',
  runtime: {
    status: 'applied', desiredRevision: 12, appliedRevision: 12,
    lastApplyAttempt: '2026-08-30T14:10:00Z', lastApplyError: '',
  },
}

const overridden: SourceConfiguration = {
  ...inherited,
  source: { sourceId: 'source-overridden', name: 'Comic Asura', language: 'en' },
  downloadConcurrency: { override: 1, effective: 1, inherited: false },
  imageRequestDelay: { override: '1250ms', effective: '1250ms', inherited: false },
  reuseBypassSession: { override: false, global: true, effective: false, inherited: false, mode: 'disposable' },
  imageConnectionMode: { override: 'reuse', global: 'fresh', effective: 'reuse', inherited: false },
  imageProxy: { optedIn: true, gatewayEnabled: true, gatewayConfigured: true, effectiveAvailable: true },
  routing: {
    socksMode: 'endpoint', socks: { endpointId: 'ep-socks', name: 'VPN SOCKS' },
    bypassMode: 'endpoint', bypass: { endpointId: 'ep-flare', name: 'VPN FlareSolverr' },
  },
  profileKey: 'vpn-comic-asura',
  runtime: {
    status: 'pending', desiredRevision: 19, appliedRevision: 18,
    lastApplyAttempt: '2026-08-30T14:24:00Z', lastApplyError: 'profile apply timed out',
  },
}

const summaries: SourceException[] = [
  { source: overridden.source, exceptionCount: 6, runtime: overridden.runtime },
  {
    source: { sourceId: 'source-proxy', name: 'Hive Scans', language: 'en' },
    exceptionCount: 1,
    runtime: { ...inherited.runtime, desiredRevision: 20, appliedRevision: 20 },
  },
  {
    source: { sourceId: 'source-error', name: 'Comix', language: 'en' },
    exceptionCount: 2,
    runtime: { ...overridden.runtime, desiredRevision: 27, appliedRevision: 26 },
  },
]

const endpoints: NetworkEndpoint[] = [
  {
    id: 'ep-socks', name: 'VPN SOCKS', kind: 'socks', enabled: true,
    host: '10.0.1.9', port: 1080, socksVersion: 5, username: '',
    url: '', session: '', sessionTtl: 0, timeout: 0, asResponseFallback: true,
  },
  {
    id: 'ep-flare', name: 'VPN FlareSolverr', kind: 'flaresolverr', enabled: true,
    host: '', port: 0, socksVersion: 5, username: '',
    url: 'http://flare:8191', session: '', sessionTtl: 15, timeout: 60, asResponseFallback: false,
  },
]

const baseProps = {
  sources: [inherited.source, ...summaries.map(summary => summary.source)],
  summaries,
  selectedSourceId: overridden.source.sourceId,
  configuration: overridden,
  endpoints,
  globalDownloadConcurrency: 5,
  globalImageRequestDelay: '500ms',
}

function definitionValue(root: Element, label: string): string {
  const term = Array.from(root.querySelectorAll('dt')).find(candidate => candidate.textContent?.trim() === label)
  expect(term, `definition term ${label}`).toBeDefined()
  const value = term!.parentElement?.querySelector('dd')?.textContent?.trim()
  expect(value, `definition value for ${label}`).toBeDefined()
  return value!
}

describe('SourceExceptionsPanel', () => {
  const fetchSpy = vi.fn()

  beforeEach(() => {
    fetchSpy.mockReset()
    vi.stubGlobal('fetch', fetchSpy)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('searches the complete catalog so a fully inherited source is discoverable', async () => {
    const wrapper = mount(SourceExceptionsPanel, { props: baseProps })
    expect(wrapper.get('input[type="search"]').attributes('aria-label')).toBe('Search installed sources')

    await wrapper.get('input[type="search"]').setValue('mangadex')

    expect(wrapper.text()).toContain('MangaDex')
    expect(wrapper.text()).toContain('Inherits all settings')
    expect(wrapper.text()).not.toContain('Hive Scans')
  })

  it('emits selection and renders exactly one canonical editor for the selected configuration', async () => {
    const wrapper = mount(SourceExceptionsPanel, { props: baseProps })
    await wrapper.get('input[type="search"]').setValue('mangadex')
    const inheritedButton = wrapper.findAll('button').find(button => button.text().includes('MangaDex'))!

    await inheritedButton.trigger('click')
    expect(wrapper.emitted('select-source')?.[0]).toEqual([inherited.source.sourceId])

    await wrapper.setProps({ selectedSourceId: inherited.source.sourceId, configuration: inherited })
    expect(wrapper.findAll('[data-testid="source-editor"]')).toHaveLength(1)
    expect(wrapper.getComponent(SourceConfigurationGroup).text()).toContain('MangaDex')
  })

  it('discards unsaved drafts when selection changes to a same-valued source', async () => {
    const sameValued: SourceConfiguration = {
      ...overridden,
      source: { sourceId: 'source-same-values', name: 'Asura Mirror', language: 'en' },
      runtime: { ...overridden.runtime, desiredRevision: 22, appliedRevision: 21 },
    }
    const wrapper = mount(SourceExceptionsPanel, { props: baseProps })
    const concurrency = wrapper.get('input[type="number"]')

    await concurrency.setValue('9')
    expect((concurrency.element as HTMLInputElement).value).toBe('9')

    await wrapper.setProps({
      sources: [...baseProps.sources, sameValued.source],
      selectedSourceId: sameValued.source.sourceId,
      configuration: sameValued,
    })

    const resetConcurrency = wrapper.get('input[type="number"]')
    expect((resetConcurrency.element as HTMLInputElement).value).toBe('1')
    expect(wrapper.findAll('[data-testid="confirmed-value"]')[0]!.text()).toBe('1')

    await wrapper.findAll('[data-testid="set-override"]')[0]!.trigger('click')
    expect(wrapper.emitted('set-override')?.[0]).toEqual([
      sameValued.source.sourceId,
      'downloadConcurrency',
      1,
    ])
  })

  it('summarises exception-bearing sources, explicit fields, and pending applies', () => {
    const wrapper = mount(SourceExceptionsPanel, { props: baseProps })

    expect(wrapper.get('[data-testid="exception-source-count"]').text()).toContain('3')
    expect(wrapper.get('[data-testid="explicit-setting-count"]').text()).toContain('9')
    expect(wrapper.get('[data-testid="pending-apply-count"]').text()).toContain('2')
  })

  it('shows the complete inherited effective configuration and approved diagnostics', async () => {
    const wrapper = mount(SourceExceptionsPanel, {
      props: { ...baseProps, selectedSourceId: inherited.source.sourceId, configuration: inherited },
    })
    const editor = wrapper.getComponent(SourceConfigurationGroup)
    const editorElement = editor.element as Element

    for (const value of ['5', '500ms', 'disposable', 'fresh', 'Off', 'Global default']) {
      expect(editor.text()).toContain(value)
    }
    expect(definitionValue(editorElement, 'Warm-up interval')).toBe('15m0s')
    expect(definitionValue(editorElement, 'Slow threshold')).toBe('5000 ms')
    expect(definitionValue(editorElement, 'Failure threshold')).toBe('5')
    expect(definitionValue(editorElement, 'Source cooldown')).toBe('30m0s')
    expect(definitionValue(editorElement, 'Politeness delay')).toBe('500ms')
    expect(definitionValue(editorElement, 'Bypass service')).toBe('Enabled')

    await editor.get('summary').trigger('click')
    for (const field of ['Profile key', 'Desired revision', 'Applied revision', 'Status', 'Last attempt', 'Sanitized error']) {
      expect(editor.text()).toContain(field)
    }
    expect(editor.text()).not.toContain('Gateway URL')
    expect(editor.text()).not.toContain('Engine response')
  })

  it('labels both inherited and overridden policy states and keeps proxy membership explicit', () => {
    const inheritedWrapper = mount(SourceExceptionsPanel, {
      props: { ...baseProps, selectedSourceId: inherited.source.sourceId, configuration: inherited },
    })
    const inheritedRows = inheritedWrapper.findAllComponents(SourceOverrideRow)
    expect(inheritedRows).toHaveLength(4)
    expect(inheritedRows.every(row => row.text().includes('Inherited'))).toBe(true)
    expect(inheritedWrapper.getComponent(SourceProxyOptInRow).text()).toContain('Off')
    expect(inheritedWrapper.getComponent(SourceProxyOptInRow).text()).not.toMatch(/inherit/i)

    const overriddenWrapper = mount(SourceExceptionsPanel, { props: baseProps })
    const overriddenRows = overriddenWrapper.findAllComponents(SourceOverrideRow)
    expect(overriddenRows).toHaveLength(4)
    expect(overriddenRows.every(row => row.text().includes('Override'))).toBe(true)
    expect(overriddenWrapper.getComponent(SourceProxyOptInRow).text()).toContain('On · active')
    expect(overriddenWrapper.getComponent(SourceProxyOptInRow).text()).not.toMatch(/inherit/i)
  })

  it('renders the server-composed global transport baseline for inherited and overridden rows', () => {
    const inheritedWrapper = mount(SourceExceptionsPanel, {
      props: { ...baseProps, selectedSourceId: inherited.source.sourceId, configuration: inherited },
    })
    const inheritedRows = inheritedWrapper.findAllComponents(SourceOverrideRow)
    expect(inheritedRows[2]!.text()).toContain('Global Off')
    expect(inheritedRows[2]!.text()).toContain('Effective Off')
    expect(inheritedRows[3]!.text()).toContain('Global fresh')
    expect(inheritedRows[3]!.text()).toContain('Effective fresh')

    const overriddenRows = mount(SourceExceptionsPanel, { props: baseProps }).findAllComponents(SourceOverrideRow)
    expect(overriddenRows[2]!.text()).toContain('Global On')
    expect(overriddenRows[2]!.text()).toContain('Effective Off')
    expect(overriddenRows[3]!.text()).toContain('Global fresh')
    expect(overriddenRows[3]!.text()).toContain('Effective reuse')
  })

  it('shows a retryable local summary failure without claiming every source inherits', async () => {
    const wrapper = mount(SourceExceptionsPanel, {
      props: { ...baseProps, summaries: [], summariesError: 'Source exceptions could not be loaded.' },
    })

    expect(wrapper.text()).toContain('Source exceptions could not be loaded.')
    expect(wrapper.text()).not.toContain('Every source currently inherits the global settings.')

    await wrapper.get('[data-testid="retry-source-summaries"]').trigger('click')
    expect(wrapper.emitted('retry-summaries')).toHaveLength(1)
  })

  it('keeps the confirmed editor mounted while a targeted mutation is confirming or reports an error', async () => {
    const wrapper = mount(SourceExceptionsPanel, {
      props: {
        ...baseProps,
        configurationPending: true,
        action: {
          sourceId: overridden.source.sourceId,
          key: 'downloadConcurrency',
          saving: true,
          error: null,
        },
      },
    })

    expect(wrapper.findComponent(SourceConfigurationGroup).exists()).toBe(true)
    expect(wrapper.get('[data-source-setting-target="downloadConcurrency"]').attributes('aria-busy')).toBe('true')

    await wrapper.setProps({
      configurationPending: false,
      configurationError: 'Confirmation read failed',
      action: {
        sourceId: overridden.source.sourceId,
        key: 'downloadConcurrency',
        saving: false,
        error: 'Confirmation read failed',
      },
    })
    expect(wrapper.findComponent(SourceConfigurationGroup).exists()).toBe(true)
    expect(wrapper.get('[data-source-setting-target="downloadConcurrency"]').text()).toContain('Confirmation read failed')
  })

  it('preserves a confirmed catalog behind a distinct retryable catalog failure', async () => {
    const wrapper = mount(SourceExceptionsPanel, {
      props: {
        ...baseProps,
        catalogLoaded: true,
        catalogError: 'Source catalog could not be loaded.',
      },
    })

    expect(wrapper.text()).toContain('Source catalog could not be loaded.')
    expect(wrapper.text()).toContain('Comic Asura')
    expect(wrapper.text()).not.toContain('No sources installed')
    await wrapper.get('[data-testid="retry-source-catalog"]').trigger('click')
    expect(wrapper.emitted('retry-catalog')).toHaveLength(1)
  })

  it('uses the no-sources message only for a successfully confirmed empty catalog', () => {
    const wrapper = mount(SourceExceptionsPanel, {
      props: {
        ...baseProps,
        sources: [],
        summaries: [],
        selectedSourceId: null,
        configuration: null,
        catalogLoaded: true,
      },
    })

    expect(wrapper.text()).toContain('No sources installed')
  })

  it('focuses an externally highlighted row and scrolls it into view', async () => {
    const scrollIntoView = vi.fn()
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true,
      value: scrollIntoView,
    })
    const wrapper = mount(SourceExceptionsPanel, {
      attachTo: document.body,
      props: { ...baseProps, highlightedSourceId: null },
    })

    await wrapper.get('input[type="search"]').setValue('comic asura')
    expect(wrapper.text()).not.toContain('Hive Scans')
    await wrapper.setProps({ highlightedSourceId: summaries[1]!.source.sourceId })
    await nextTick()

    const highlighted = wrapper.get('[data-highlighted="true"]')
    expect(document.activeElement).toBe(highlighted.element)
    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth', block: 'nearest' })
    wrapper.unmount()
  })

  it('forwards proxy membership and both routing mutation payloads for the selected source', async () => {
    const wrapper = mount(SourceExceptionsPanel, { props: baseProps })

    await wrapper.getComponent(SourceProxyOptInRow).get('[role="switch"]').trigger('click')
    expect(wrapper.emitted('set-override')?.[0]).toEqual([
      overridden.source.sourceId,
      'imageProxy',
      false,
    ])

    const routing = wrapper.getComponent(SourceBindingRow)
    await routing.get(`select[aria-label="SOCKS route for ${overridden.source.name}"]`).setValue('')
    expect(wrapper.emitted('set-binding')?.[0]).toEqual([{
      sourceId: overridden.source.sourceId,
      socksEndpointId: null,
      flareMode: 'endpoint',
      flareEndpointId: 'ep-flare',
    }])

    await routing.findAll('button').find(button => button.text() === 'Use global default')!.trigger('click')
    expect(wrapper.emitted('clear-binding')?.[0]).toEqual([overridden.source.sourceId])
  })

  it('emits keyed row mutations upward and isolates saving/error state to that row', () => {
    const wrapper = mount(SourceExceptionsPanel, {
      props: {
        ...baseProps,
        action: {
          sourceId: overridden.source.sourceId,
          key: 'downloadConcurrency',
          saving: true,
          error: 'Concurrency could not be saved.',
        },
      },
    })
    const rows = wrapper.findAllComponents(SourceOverrideRow)

    expect(rows[0]!.props('saving')).toBe(true)
    expect(rows[0]!.props('error')).toBe('Concurrency could not be saved.')
    expect(rows.slice(1).every(row => row.props('saving') === false && row.props('error') === null)).toBe(true)

    rows[1]!.vm.$emit('set-override', 'imageRequestDelay', '900ms')
    expect(wrapper.emitted('set-override')?.[0]).toEqual([
      overridden.source.sourceId,
      'imageRequestDelay',
      '900ms',
    ])
  })

  it('never starts an API request when selecting or mutating presentational rows', async () => {
    const wrapper = mount(SourceExceptionsPanel, { props: baseProps })

    wrapper.findAllComponents(SourceOverrideRow)[0]!.vm.$emit('use-global', 'downloadConcurrency')
    await wrapper.findAll('button').find(button => button.text().includes('Hive Scans'))!.trigger('click')

    expect(fetchSpy).not.toHaveBeenCalled()
    expect(wrapper.emitted('select-source')).toBeTruthy()
    expect(wrapper.emitted('use-global')?.[0]).toEqual([
      overridden.source.sourceId,
      'downloadConcurrency',
    ])
  })
})
