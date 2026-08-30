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
import SourceOverrideRow from './SourceOverrideRow.vue'
import SourceProxyOptInRow from './SourceProxyOptInRow.vue'
import type { components } from '../../utils/api/schema.d.ts'
import type { NetworkEndpoint } from '../screens/settings.types'

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
  reuseBypassSession: { override: null, effective: true, inherited: true, mode: 'reusable' },
  imageConnectionMode: { override: null, effective: 'reuse', inherited: true },
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
  reuseBypassSession: { override: false, effective: false, inherited: false, mode: 'disposable' },
  imageConnectionMode: { override: 'fresh', effective: 'fresh', inherited: false },
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
  globalReuseBypassSession: true,
  globalImageConnectionMode: 'reuse' as const,
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

    for (const value of ['5', '500ms', '15m0s', '5000', '30m0s', 'reusable', 'reuse', 'Off', 'Global default']) {
      expect(editor.text()).toContain(value)
    }

    await editor.get('summary').trigger('click')
    for (const field of ['Profile key', 'Desired revision', 'Applied revision', 'Status', 'Last attempt', 'Sanitized error']) {
      expect(editor.text()).toContain(field)
    }
    expect(editor.text()).not.toContain('Gateway URL')
    expect(editor.text()).not.toContain('Engine response')
  })

  it('labels inherited and overridden policy rows and keeps proxy membership explicit', () => {
    const wrapper = mount(SourceExceptionsPanel, { props: baseProps })
    const rows = wrapper.findAllComponents(SourceOverrideRow)

    expect(rows).toHaveLength(4)
    expect(rows.every(row => row.text().includes('Override'))).toBe(true)
    expect(wrapper.getComponent(SourceProxyOptInRow).text()).toContain('On · active')
    expect(wrapper.getComponent(SourceProxyOptInRow).text()).not.toMatch(/inherit/i)
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

    await wrapper.setProps({ highlightedSourceId: summaries[1]!.source.sourceId })
    await nextTick()

    const highlighted = wrapper.get('[data-highlighted="true"]')
    expect(document.activeElement).toBe(highlighted.element)
    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth', block: 'nearest' })
    wrapper.unmount()
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
