import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, userEvent, within } from 'storybook/test'
import { ref } from 'vue'
import SegmentedTabs from './SegmentedTabs.vue'

/**
 * Stories for the SegmentedTabs count-pill tab bar. The interactive stories wire
 * a local ref so clicking a tab moves the accent fill and the count-pill
 * highlight. Flip the theme toolbar to confirm both themes.
 */
const meta = {
  title: 'UI/SegmentedTabs',
  component: SegmentedTabs,
  // modelValue + tabs are required props; each story renders its own live-ref
  // wrapper, so these defaults only satisfy the CSF3 story typing.
  args: {
    modelValue: 'active',
    tabs: [
      { key: 'active', label: 'Active', count: 3 },
      { key: 'failed', label: 'Failed', count: 12 },
    ],
  },
} satisfies Meta<typeof SegmentedTabs>

export default meta
type Story = StoryObj<typeof meta>

/** Three tabs, each with a count — clicking switches the active pill. */
export const WithCounts: Story = {
  render: () => ({
    components: { SegmentedTabs },
    setup: () => ({
      value: ref('active'),
      tabs: [
        { key: 'active', label: 'Active', count: 3 },
        { key: 'failed', label: 'Failed', count: 12 },
        { key: 'queued', label: 'Queued', count: 0 },
      ],
    }),
    template: '<SegmentedTabs v-model="value" :tabs="tabs" />',
  }),
}

/** The LibraryList category filter — counts present, the "All" tab leads. */
export const CategoryFilter: Story = {
  render: () => ({
    components: { SegmentedTabs },
    setup: () => ({
      value: ref('all'),
      tabs: [
        { key: 'all', label: 'All', count: 84 },
        { key: 'manga', label: 'Manga', count: 41 },
        { key: 'manhwa', label: 'Manhwa', count: 28 },
        { key: 'manhua', label: 'Manhua', count: 15 },
      ],
    }),
    template: '<SegmentedTabs v-model="value" :tabs="tabs" />',
  }),
}

/** Tabs without counts — `count` is optional, so no trailing pill renders. */
export const NoCounts: Story = {
  render: () => ({
    components: { SegmentedTabs },
    setup: () => ({
      value: ref('repos'),
      tabs: [
        { key: 'repos', label: 'Repositories' },
        { key: 'installed', label: 'Installed' },
        { key: 'available', label: 'Available' },
      ],
    }),
    template: '<SegmentedTabs v-model="value" :tabs="tabs" />',
  }),
}

/** Panel-linked tabs demonstrate automatic keyboard activation and roving focus. */
export const PanelNavigation: Story = {
  render: () => ({
    components: { SegmentedTabs },
    setup: () => ({
      value: ref('scheduling'),
      tabs: [
        { key: 'scheduling', label: 'Scheduling', id: 'scheduling-tab', panelId: 'scheduling-panel' },
        { key: 'protection', label: 'Protection', id: 'protection-tab', panelId: 'protection-panel' },
        { key: 'routing', label: 'Routing', id: 'routing-tab', panelId: 'routing-panel' },
      ],
    }),
    template: `
      <div>
        <SegmentedTabs v-model="value" :tabs="tabs" accessible-label="Download engine sections" />
        <p id="scheduling-panel" role="tabpanel" aria-labelledby="scheduling-tab" :hidden="value !== 'scheduling'">Scheduling controls</p>
        <p id="protection-panel" role="tabpanel" aria-labelledby="protection-tab" :hidden="value !== 'protection'">Protection controls</p>
        <p id="routing-panel" role="tabpanel" aria-labelledby="routing-tab" :hidden="value !== 'routing'">Routing controls</p>
      </div>
    `,
  }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const scheduling = canvas.getByRole('tab', { name: 'Scheduling' })
    scheduling.focus()
    await userEvent.keyboard('{ArrowLeft}')
    await expect(canvas.getByRole('tab', { name: 'Routing' })).toHaveFocus()
    await expect(canvas.getByRole('tabpanel', { name: 'Routing' })).toBeVisible()
    await userEvent.keyboard('{Home}')
    await expect(scheduling).toHaveFocus()
  },
}
