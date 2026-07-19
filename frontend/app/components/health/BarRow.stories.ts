import type { Meta, StoryObj } from '@storybook/vue3'
import BarRow from './BarRow.vue'

/**
 * Stories for one leaderboard bar. The parent normally sets `fraction` against a
 * shared max; these show individual fills. Flip the theme toolbar.
 */
const meta = {
  title: 'Health/BarRow',
  component: BarRow,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof BarRow>

export default meta
type Story = StoryObj<typeof meta>

export const Full: Story = {
  args: { label: 'ComicK', fraction: 1, valueLabel: '18.4s', tone: 'var(--set-update-text)' },
}

export const Partial: Story = {
  args: { label: 'Asura Scans', fraction: 0.42, valueLabel: '4.2s', tone: 'var(--set-update-text)' },
}

export const Danger: Story = {
  args: { label: 'ComicK', fraction: 0.8, valueLabel: '14×', tone: 'var(--danger)' },
}

/** A tiny non-zero value still shows a visible sliver. */
export const Sliver: Story = {
  args: { label: 'MangaDex', fraction: 0.01, valueLabel: '240ms', tone: 'var(--set-update-text)' },
}
