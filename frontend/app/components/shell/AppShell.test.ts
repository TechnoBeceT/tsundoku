/**
 * AppShell — the two "Health" entry points must be DISTINGUISHABLE.
 *
 * The header "N need attention" pill is a SERIES-health signal, so it must land
 * the owner on the Health console's Library (sick-series) tab regardless of the
 * tab they last viewed. It therefore emits `navigate('health?tab=library')` — an
 * explicit forced-tab deep-link the page resolver honours over the persisted
 * sessionStorage tab (see healthTabs.test.ts). The nav-rail Health item, by
 * contrast, emits plain `navigate('health')` so it RESTORES the stored tab.
 *
 * Non-vacuous: revert the pill to `emit('navigate', 'health')` and the first
 * assertion fails (the two entry points become indistinguishable → the pill can
 * wrongly land on the Sources tab); change the nav-rail key and the second fails.
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

function mountShell(unhealthy: number) {
  return mount(AppShell, {
    props: {
      navItems,
      activeRoute: 'library',
      theme: 'dark' as const,
      headerTitle: 'Library',
      unhealthy,
    },
  })
}

describe('AppShell health entry points', () => {
  it('attention pill forces the Library tab via ?tab=library', async () => {
    const wrapper = mountShell(3)
    await wrapper.get('.head__attention').trigger('click')
    expect(wrapper.emitted('navigate')?.[0]).toEqual(['health?tab=library'])
  })

  it('nav-rail Health item emits plain "health" (stored tab restored)', async () => {
    const wrapper = mountShell(3)
    await wrapper.get('button[aria-label="Health"]').trigger('click')
    expect(wrapper.emitted('navigate')?.[0]).toEqual(['health'])
  })
})
