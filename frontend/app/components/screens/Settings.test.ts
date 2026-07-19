/**
 * Settings screen — the source-metrics relocation (Source Health Console, slice 3).
 *
 * Pins that the per-source search-metrics UI is GONE from the Settings "sources"
 * pane (it moved to the /health Source Health tab) while the source-politeness
 * CONFIG knobs stay:
 *   1. the sources pane still renders SourcesSettingsPane (the config knobs);
 *   2. it no longer renders SourceMetricsPane (relocated).
 *
 * Non-vacuous: re-mount SourceMetricsPane in the sources pane and test 2 fails;
 * drop SourcesSettingsPane and test 1 fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Settings from './Settings.vue'
import SourcesSettingsPane from '../settings/SourcesSettingsPane.vue'
import SourceMetricsPane from '../health/SourceMetricsPane.vue'
import {
  availableExtensions,
  engineInfo,
  extCheckInterval,
  flareSolverrConfig,
  installedExtensions,
  librarySettings,
  repos,
  settingsCategories,
  sourcesSettings,
  systemInfo,
} from '../../fixtures/settings'

/** Mount the screen straight onto its "sources" pane with the required props. */
function mountSourcesPane() {
  return mount(Settings, {
    props: {
      activePane: 'sources',
      library: librarySettings,
      system: systemInfo,
      categories: settingsCategories,
      engine: engineInfo,
      flareSolverr: flareSolverrConfig,
      extensions: installedExtensions,
      availableExtensions,
      repos,
      extCheckInterval,
      sourcesSettings,
    },
  })
}

describe('Settings screen — source-metrics relocation', () => {
  it('still renders SourcesSettingsPane (the config knobs) on the sources pane', () => {
    const wrapper = mountSourcesPane()
    expect(wrapper.findComponent(SourcesSettingsPane).exists()).toBe(true)
  })

  it('no longer renders SourceMetricsPane on the sources pane (moved to /health)', () => {
    const wrapper = mountSourcesPane()
    expect(wrapper.findComponent(SourceMetricsPane).exists()).toBe(false)
  })
})
