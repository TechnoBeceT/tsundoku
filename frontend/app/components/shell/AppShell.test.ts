/**
 * AppShell — the two "Health" entry points must be DISTINGUISHABLE, and the
 * source-outage danger pill must be a THIRD, distinct signal.
 *
 * The header "N need attention" pill is a SERIES-health signal, so it must land
 * the owner on the Health console's Library (sick-series) tab regardless of the
 * tab they last viewed. It therefore emits `navigate('health?tab=library')` — an
 * explicit forced-tab deep-link the page resolver honours over the persisted
 * sessionStorage tab (see healthTabs.test.ts). The nav-rail Health item, by
 * contrast, emits plain `navigate('health')` so it RESTORES the stored tab.
 *
 * The "N sources down" pill is a live SOURCE-outage signal (a circuit-breaker
 * tripped now); it lands on the Sources tab via `navigate('health?tab=sources')`
 * and is a SEPARATE element from the amber series pill (both can show at once).
 *
 * Non-vacuous: revert the series pill to `emit('navigate', 'health')` and the
 * first assertion fails; change the nav-rail key and the second fails; point the
 * source pill at `?tab=library` and its assertion fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import AppShell from './AppShell.vue'
import type { NavItem } from './types'

const navItems: NavItem[] = [
  { key: 'library', label: 'Library', icon: 'book' },
  { key: 'health', label: 'Health', icon: 'activity' },
  { key: 'settings', label: 'Settings', icon: 'settings', pinned: true },
]

function mountShell(props: { unhealthy?: number, erroringSources?: number } = {}) {
  return mount(AppShell, {
    props: {
      navItems,
      activeRoute: 'library',
      theme: 'dark' as const,
      headerTitle: 'Library',
      ...props,
    },
  })
}

describe('AppShell health entry points', () => {
  it('attention pill forces the Library tab via ?tab=library', async () => {
    const wrapper = mountShell({ unhealthy: 3 })
    await wrapper.get('.head__attention').trigger('click')
    expect(wrapper.emitted('navigate')?.[0]).toEqual(['health?tab=library'])
  })

  it('nav-rail Health item emits plain "health" (stored tab restored)', async () => {
    const wrapper = mountShell({ unhealthy: 3 })
    await wrapper.get('button[aria-label="Health"]').trigger('click')
    expect(wrapper.emitted('navigate')?.[0]).toEqual(['health'])
  })
})

describe('AppShell source-outage alert', () => {
  it('source pill forces the Sources tab via ?tab=sources', async () => {
    const wrapper = mountShell({ erroringSources: 2 })
    await wrapper.get('.head__source-alert').trigger('click')
    expect(wrapper.emitted('navigate')?.[0]).toEqual(['health?tab=sources'])
  })

  it('is hidden when no sources are erroring', () => {
    const wrapper = mountShell({ erroringSources: 0 })
    expect(wrapper.find('.head__source-alert').exists()).toBe(false)
  })

  it('is a SEPARATE pill from the amber series pill — both show at once', () => {
    const wrapper = mountShell({ unhealthy: 3, erroringSources: 2 })
    // Two distinct elements: the source-outage danger chip and the series pill.
    expect(wrapper.find('.head__source-alert').exists()).toBe(true)
    expect(wrapper.find('.head__attention').exists()).toBe(true)
    // Each carries its own count + destination — the two signals never conflate.
    expect(wrapper.get('.head__source-alert').text()).toContain('2 sources down')
    expect(wrapper.get('.head__attention').text()).toContain('3 need attention')
  })
})
