/**
 * SourceMetricRow — the cooling-down (anti-ban breaker) state + Reset action.
 *
 * Pins that a source whose circuit-breaker is tripped renders the cooldown banner
 * ("cooling down · retry ~Nm (N failures)", the breaker error in the tooltip) with
 * a Reset button that emits `reset` carrying the source id, and that a healthy
 * source shows neither the banner nor the button.
 *
 * Non-vacuous: drop the breaker banner and the first assertion fails; drop the
 * `@click="emit('reset', source.id)"` and the emit assertion fails; leak the
 * banner onto a healthy row and the last test fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SourceMetricRow from './SourceMetricRow.vue'
import type { SourceMetric } from '../screens/sourceHealth.types'

// A base healthy snapshot; individual tests override the breaker.
function metric(overrides: Partial<SourceMetric> = {}): SourceMetric {
  return {
    id: 'src-comix',
    name: 'Comix',
    avgLatencyMs: 1800,
    lastLatencyMs: 2000,
    searchCount: 80,
    successCount: 40,
    failCount: 40,
    lastError: '',
    lastErrorAt: null,
    lastSuccessAt: null,
    lastWarmedAt: null,
    updatedAt: '2026-07-05T10:00:00Z',
    isSlow: false,
    breaker: null,
    ...overrides,
  }
}

describe('SourceMetricRow', () => {
  it('renders the cooldown banner + a Reset button when the breaker is tripped', () => {
    const source = metric({
      breaker: {
        consecutiveFailures: 5,
        cooldownUntil: new Date(Date.now() + 28 * 60_000).toISOString(),
        lastError: 'Cloudflare challenge failed (403)',
        isCoolingDown: true,
      },
    })
    const wrapper = mount(SourceMetricRow, { props: { source } })

    const text = wrapper.text()
    expect(text).toContain('cooling down')
    expect(text).toContain('retry ~28m') // live ETA from cooldownUntil
    expect(text).toContain('5 failures')
    // The breaker's last error rides in the banner tooltip.
    expect(wrapper.find('.metric__cooldown-text').attributes('title')).toBe('Cloudflare challenge failed (403)')
    // The Reset button is present.
    expect(wrapper.findAll('button').some(b => b.text().includes('Reset'))).toBe(true)
    wrapper.unmount()
  })

  it('emits reset with the source id when Reset is clicked', async () => {
    const source = metric({
      breaker: { consecutiveFailures: 3, cooldownUntil: new Date(Date.now() + 60_000).toISOString(), lastError: '', isCoolingDown: true },
    })
    const wrapper = mount(SourceMetricRow, { props: { source } })

    const reset = wrapper.findAll('button').find(b => b.text().includes('Reset'))!
    await reset.trigger('click')

    const emitted = wrapper.emitted('reset')
    expect(emitted).toBeTruthy()
    expect(emitted![0]).toEqual(['src-comix'])
    wrapper.unmount()
  })

  it('shows no cooldown banner or Reset button for a healthy source', () => {
    const wrapper = mount(SourceMetricRow, { props: { source: metric() } })
    expect(wrapper.find('.metric__cooldown').exists()).toBe(false)
    expect(wrapper.findAll('button').some(b => b.text().includes('Reset'))).toBe(false)
    wrapper.unmount()
  })
})
