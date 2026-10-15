import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SourceConfigurationGroup from './SourceConfigurationGroup.vue'
import { fullyInheritedSourceConfiguration } from '../../fixtures/settings'
import type { components } from '../../utils/api/schema.d.ts'

type SourceConfiguration = components['schemas']['SourceEffectiveConfiguration']

function configuration(kcef: SourceConfiguration['kcef']): SourceConfiguration {
  return {
    ...fullyInheritedSourceConfiguration,
    kcef,
  }
}

describe('SourceConfigurationGroup', () => {

  function socksConfiguration(kcef: SourceConfiguration['kcef'] = fullyInheritedSourceConfiguration.kcef): SourceConfiguration {
    return {
      ...configuration(kcef),
      routing: {
        ...fullyInheritedSourceConfiguration.routing,
        socksMode: 'endpoint',
        socks: { endpointId: 'vpn-endpoint', name: 'VPN SOCKS' },
      },
    }
  }

  it('explains effective SOCKS routing and disables Required without blocking Auto or Disabled', async () => {
    const wrapper = mount(SourceConfigurationGroup, { props: { configuration: socksConfiguration() } })
    const row = wrapper.get('[data-source-setting-target="kcefPolicy"]')
    expect(row.text()).toContain('Required is unavailable because this source uses SOCKS routing. The embedded browser cannot use that route.')
    expect(row.get('option[value="required"]').attributes('disabled')).toBeDefined()
    expect(row.get('option[value="auto"]').attributes('disabled')).toBeUndefined()
    expect(row.get('option[value="disabled"]').attributes('disabled')).toBeUndefined()

    for (const value of ['auto', 'disabled']) {
      await row.get('select').setValue(value)
      await row.get('[data-testid="set-override"]').trigger('click')
    }
    expect(wrapper.emitted('set-override')).toEqual([
      [fullyInheritedSourceConfiguration.source.sourceId, 'kcefPolicy', 'auto'],
      [fullyInheritedSourceConfiguration.source.sourceId, 'kcefPolicy', 'disabled'],
    ])
    expect(wrapper.emitted('set-binding')).toBeUndefined()
    expect(wrapper.emitted('clear-binding')).toBeUndefined()
  })

  it('blocks a pending Required draft when effective routing changes to SOCKS without rewriting confirmed policy', async () => {
    const wrapper = mount(SourceConfigurationGroup, { props: { configuration: fullyInheritedSourceConfiguration } })
    const row = wrapper.get('[data-source-setting-target="kcefPolicy"]')
    await row.get('select').setValue('required')
    await wrapper.setProps({ configuration: socksConfiguration() })

    expect(row.get('[data-testid="confirmed-value"]').text()).toBe('auto')
    expect((row.get('select').element as HTMLSelectElement).value).toBe('required')
    expect(row.get('[data-testid="set-override"]').attributes('disabled')).toBeDefined()
    await row.get('[data-testid="set-override"]').trigger('click')
    expect(wrapper.emitted('set-override')).toBeUndefined()

    await wrapper.setProps({ configuration: fullyInheritedSourceConfiguration })
    expect(row.get('[data-testid="set-override"]').attributes('disabled')).toBeUndefined()
    await row.get('[data-testid="set-override"]').trigger('click')
    expect(wrapper.emitted('set-override')?.[0]).toEqual([
      fullyInheritedSourceConfiguration.source.sourceId, 'kcefPolicy', 'required',
    ])
  })

  it('keeps a stored Required policy visible under SOCKS until the owner selects a compatible policy', async () => {
    const wrapper = mount(SourceConfigurationGroup, {
      props: { configuration: socksConfiguration({ override: 'required', global: 'auto', effective: 'required', inherited: false, enabled: false }) },
    })
    const row = wrapper.get('[data-source-setting-target="kcefPolicy"]')
    expect(row.get('[data-testid="confirmed-value"]').text()).toBe('required')
    expect((row.get('select').element as HTMLSelectElement).value).toBe('required')
    expect(row.get('[data-testid="set-override"]').attributes('disabled')).toBeDefined()
    expect(wrapper.emitted('set-override')).toBeUndefined()
    await row.get('select').setValue('disabled')
    await row.get('[data-testid="set-override"]').trigger('click')
    expect(wrapper.emitted('set-override')?.[0]?.[2]).toBe('disabled')
    expect(row.get('[data-testid="confirmed-value"]').text()).toBe('required')
  })

  it('restores the global Auto baseline under effective SOCKS', async () => {
    const wrapper = mount(SourceConfigurationGroup, {
      props: { configuration: socksConfiguration({ override: 'disabled', global: 'auto', effective: 'disabled', inherited: false, enabled: false }) },
    })
    const row = wrapper.get('[data-source-setting-target="kcefPolicy"]')
    expect(row.get('[data-testid="use-global"]').attributes('disabled')).toBeUndefined()
    await row.get('[data-testid="use-global"]').trigger('click')
    expect(wrapper.emitted('use-global')).toEqual([[fullyInheritedSourceConfiguration.source.sourceId, 'kcefPolicy']])
  })

  it('allows Required when a stored SOCKS endpoint is unavailable and the effective route has fallen back', async () => {
    const current: SourceConfiguration = {
      ...fullyInheritedSourceConfiguration,
      routing: {
        ...fullyInheritedSourceConfiguration.routing,
        stored: { ...fullyInheritedSourceConfiguration.routing.stored, configured: true, socksMode: 'endpoint', socks: { endpointId: 'unavailable', name: null } },
      },
    }
    const wrapper = mount(SourceConfigurationGroup, { props: { configuration: current } })
    const row = wrapper.get('[data-source-setting-target="kcefPolicy"]')
    expect(row.get('option[value="required"]').attributes('disabled')).toBeUndefined()
    expect(row.text()).not.toContain('Required is unavailable because this source uses SOCKS routing.')
    await row.get('select').setValue('required')
    await row.get('[data-testid="set-override"]').trigger('click')
    expect(wrapper.emitted('set-override')?.[0]?.[2]).toBe('required')
  })

  it('shows the configured Required policy and its effective embedded-browser state', () => {
    const wrapper = mount(SourceConfigurationGroup, {
      props: {
        configuration: configuration({
          override: 'required', global: 'auto', effective: 'required', inherited: false, enabled: true,
        }),
      },
    })

    const row = wrapper.get('[data-source-setting-target="kcefPolicy"]')
    expect(row.text()).toContain('Embedded browser')
    expect(row.text()).toContain('Override')
    expect(row.text()).toContain('Global auto')
    expect(row.text()).toContain('Effective required')
    expect(row.text()).toContain('Effective embedded browser: On')
    expect(row.text()).toContain('Required is incompatible with SOCKS routing.')
  })

  it.each([
    ['auto', false],
    ['required', true],
    ['disabled', false],
  ] as const)('emits the %s override through the existing row control', async (value, enabled) => {
    const wrapper = mount(SourceConfigurationGroup, {
      props: {
        configuration: configuration({ override: value, global: 'auto', effective: value, inherited: false, enabled }),
      },
    })

    await wrapper.get('[data-source-setting-target="kcefPolicy"] select').setValue(value)
    await wrapper.get('[data-source-setting-target="kcefPolicy"] [data-testid="set-override"]').trigger('click')

    expect(wrapper.emitted('set-override')).toEqual([[
      fullyInheritedSourceConfiguration.source.sourceId,
      'kcefPolicy',
      value,
    ]])
    expect(wrapper.get('[data-source-setting-target="kcefPolicy"]').text())
      .toContain(`Effective embedded browser: ${enabled ? 'On' : 'Off'}`)
  })

  it('labels inherited Auto separately from the effective embedded-browser state', () => {
    const wrapper = mount(SourceConfigurationGroup, {
      props: {
        configuration: configuration({ override: null, global: 'auto', effective: 'auto', inherited: true, enabled: true }),
      },
    })

    const row = wrapper.get('[data-source-setting-target="kcefPolicy"]')
    expect(row.text()).toContain('Inherited')
    expect(row.text()).toContain('Effective auto')
    expect(row.text()).toContain('Effective embedded browser: On')
    expect(row.get('[data-testid="use-global"]').attributes('disabled')).toBeDefined()
  })

  it('restores inherited embedded-browser policy from an override', async () => {
    const wrapper = mount(SourceConfigurationGroup, {
      props: {
        configuration: configuration({ override: 'required', global: 'auto', effective: 'required', inherited: false, enabled: true }),
      },
    })

    const row = wrapper.get('[data-source-setting-target="kcefPolicy"]')
    const useGlobal = row.get('[data-testid="use-global"]')
    expect(useGlobal.attributes('disabled')).toBeUndefined()

    await useGlobal.trigger('click')

    expect(wrapper.emitted('use-global')).toEqual([[
      fullyInheritedSourceConfiguration.source.sourceId,
      'kcefPolicy',
    ]])
  })

  it('keeps pending apply status and sanitized apply errors distinct from the policy intent', async () => {
    const wrapper = mount(SourceConfigurationGroup, {
      props: {
        configuration: {
          ...configuration({ override: 'required', global: 'auto', effective: 'required', inherited: false, enabled: true }),
          runtime: {
            status: 'pending',
            desiredRevision: 13,
            appliedRevision: 12,
            lastApplyAttempt: '2026-09-01T12:00:00Z',
            lastApplyError: 'The embedded browser could not start.',
          },
        },
      },
    })

    expect(wrapper.text()).toContain('Pending')
    expect(wrapper.text()).toContain('12 → 13')
    await wrapper.get('summary').trigger('click')
    expect(wrapper.text()).toContain('The embedded browser could not start.')
  })

  it('keeps keyboard semantics and row-local pending or error feedback', async () => {
    const wrapper = mount(SourceConfigurationGroup, {
      attachTo: document.body,
      props: {
        configuration: configuration({ override: 'disabled', global: 'auto', effective: 'disabled', inherited: false, enabled: false }),
        action: {
          sourceId: fullyInheritedSourceConfiguration.source.sourceId,
          key: 'kcefPolicy',
          saving: true,
          error: 'Embedded browser policy could not be saved.',
        },
      },
    })

    const row = wrapper.get('[data-source-setting-target="kcefPolicy"]')
    const select = row.get('select[aria-label="Embedded browser override"]')
    expect(row.attributes('aria-busy')).toBe('true')
    expect(select.attributes('disabled')).toBeDefined()
    expect(row.text()).toContain('Embedded browser policy could not be saved.')
    await wrapper.setProps({
      action: {
        sourceId: fullyInheritedSourceConfiguration.source.sourceId,
        key: 'kcefPolicy',
        saving: false,
        error: 'Embedded browser policy could not be saved.',
      },
    })
    const selectElement = select.element as HTMLSelectElement
    selectElement.focus()
    expect(document.activeElement).toBe(selectElement)
    wrapper.unmount()
  })
})
