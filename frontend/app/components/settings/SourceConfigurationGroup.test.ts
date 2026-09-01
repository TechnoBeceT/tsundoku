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
    expect(row.text()).toContain('Enabled')
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
    expect(row.text()).toContain('Enabled')
    expect(row.get('[data-testid="use-global"]').attributes('disabled')).toBeDefined()
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
