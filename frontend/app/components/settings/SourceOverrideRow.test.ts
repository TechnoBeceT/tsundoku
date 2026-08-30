/**
 * Source-setting row contracts.
 *
 * Mutations caught here:
 *   - dropping the setting key or coercing a numeric override to a string;
 *   - clearing the wrong field instead of emitting the keyed global reset;
 *   - disabling an entire source panel rather than only the saving row;
 *   - replacing the last confirmed value with a failed optimistic edit;
 *   - describing image proxy membership as ordinary inheritance;
 *   - collapsing applied and pending runtime states into the same status.
 */
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SourceOverrideRow from './SourceOverrideRow.vue'
import SourceProxyOptInRow from './SourceProxyOptInRow.vue'
import SourceApplyStatus from './SourceApplyStatus.vue'

const overrideProps = {
  settingKey: 'downloadConcurrency' as const,
  name: 'Chapter concurrency',
  hint: 'Maximum chapter downloads this source may run at once',
  control: 'number' as const,
  modelValue: 5,
  globalValue: 5,
  inherited: true,
}

describe('SourceOverrideRow', () => {
  it('emits a keyed numeric override from the explicit save action', async () => {
    const wrapper = mount(SourceOverrideRow, { props: overrideProps })

    await wrapper.get('input').setValue('2')
    await wrapper.get('[data-testid="set-override"]').trigger('click')

    expect(wrapper.emitted('set-override')?.[0]).toEqual(['downloadConcurrency', 2])
  })

  it('clears only this setting through the keyed use-global action', async () => {
    const wrapper = mount(SourceOverrideRow, {
      props: { ...overrideProps, modelValue: 2, inherited: false },
    })

    await wrapper.get('[data-testid="use-global"]').trigger('click')

    expect(wrapper.emitted('use-global')?.[0]).toEqual(['downloadConcurrency'])
    expect(wrapper.emitted('set-override')).toBeUndefined()
  })

  it('disables this row controls and actions while its save is in flight', () => {
    const wrapper = mount(SourceOverrideRow, {
      props: { ...overrideProps, modelValue: 2, inherited: false, saving: true },
    })

    expect(wrapper.get('input').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button')).toHaveLength(2)
    expect(wrapper.findAll('button').every(button => button.attributes('disabled') !== undefined)).toBe(true)
  })

  it('keeps the confirmed value visible when a failed edit adds an error', async () => {
    const wrapper = mount(SourceOverrideRow, {
      props: { ...overrideProps, modelValue: 2, inherited: false },
    })

    await wrapper.get('input').setValue('9')
    await wrapper.setProps({ error: 'Concurrency must be between 1 and 20.' })

    expect(wrapper.get('[data-testid="confirmed-value"]').text()).toBe('2')
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('9')
    expect(wrapper.get('[role="alert"]').text()).toContain('between 1 and 20')
  })
})

describe('SourceProxyOptInRow', () => {
  it('uses explicit On/Off safety language and emits keyed proxy membership', async () => {
    const wrapper = mount(SourceProxyOptInRow, {
      props: { enabled: false, effectiveAvailable: false },
    })

    expect(wrapper.text()).toContain('Off')
    expect(wrapper.text()).toContain('native image path')
    expect(wrapper.text()).not.toMatch(/inherit|global default|use global/i)

    await wrapper.get('[role="switch"]').trigger('click')
    expect(wrapper.emitted('set-override')?.[0]).toEqual(['imageProxy', true])
  })

  it('disables only the proxy action while its row is saving', () => {
    const wrapper = mount(SourceProxyOptInRow, {
      props: { enabled: true, effectiveAvailable: true, saving: true },
    })

    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('On')
  })
})

describe('SourceApplyStatus', () => {
  it('distinguishes an applied revision from a pending one', () => {
    const applied = mount(SourceApplyStatus, {
      props: {
        runtime: {
          status: 'applied',
          desiredRevision: 12,
          appliedRevision: 12,
          lastApplyAttempt: '2026-08-30T14:10:00Z',
          lastApplyError: '',
        },
      },
    })
    const pending = mount(SourceApplyStatus, {
      props: {
        runtime: {
          status: 'pending',
          desiredRevision: 13,
          appliedRevision: 12,
          lastApplyAttempt: null,
          lastApplyError: '',
        },
      },
    })

    expect(applied.text()).toContain('Applied')
    expect(applied.text()).toContain('Revision 12')
    expect(pending.text()).toContain('Pending')
    expect(pending.text()).toContain('12 → 13')
  })
})
