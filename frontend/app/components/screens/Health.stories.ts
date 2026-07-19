import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import Health from './Health.vue'
import type { HealthTab } from '~/utils/healthTabs'
import { sickSeries } from '../../fixtures/libraryHealth'
import { sourceMetrics } from '../../fixtures/settings'
// Load both palettes the tabs need directly (Library-tab health badges live in
// seriesDetail.css; Source-tab metric badges in settings.css) so each tab reads
// correctly in isolation. The live app pulls both via index.css.
import '../../assets/css/tokens/seriesDetail.css'
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Health console — the top-level `/health` 2-tab screen. Each
 * story opens on a tab; the tab bar stays interactive (clicking switches tabs)
 * via a live `activeTab` ref, mirroring the Settings screen's controlled-pane
 * stories. Flip the theme toolbar to confirm both tabs read in dark + light.
 */
const baseProps = {
  series: sickSeries,
  metrics: sourceMetrics,
}

const meta = {
  title: 'Screens/Health',
  component: Health,
  parameters: { layout: 'fullscreen' },
  args: baseProps,
} satisfies Meta<typeof Health>

export default meta
type Story = StoryObj<typeof meta>

/** Renders the console with a live `activeTab` so the tab bar actually switches. */
const withTab = (startTab: HealthTab) => ({
  components: { Health },
  setup() {
    const activeTab = ref<HealthTab>(startTab)
    return { activeTab, baseProps }
  },
  template: `
    <Health
      v-bind="baseProps"
      :active-tab="activeTab"
      @set-tab="activeTab = $event"
    />
  `,
})

/** Library tab — the series-centric "what needs attention" report. */
export const LibraryTab: Story = {
  render: () => withTab('library'),
}

/** Sources tab — the source-centric search-metrics report. */
export const SourcesTab: Story = {
  render: () => withTab('sources'),
}
