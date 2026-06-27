import type { Meta, StoryObj } from '@storybook/vue3'
import Stepper from './Stepper.vue'

/**
 * Stories for the Stepper. Shows the horizontal pill-and-connector layout
 * mid-flow (one done, one active, one todo) and the vertical dot layout with a
 * done/active/todo mix plus `sub` lines. Flip the theme toolbar to confirm both
 * themes.
 */
const meta = {
  title: 'UI/Stepper',
  component: Stepper,
} satisfies Meta<typeof Stepper>

export default meta
type Story = StoryObj<typeof meta>

/** Horizontal, mid-flow — "Configure" is active, "Search" is done. */
export const HorizontalMidFlow: Story = {
  render: () => ({
    components: { Stepper },
    setup: () => ({
      steps: [
        { key: 'search', label: 'Search' },
        { key: 'configure', label: 'Configure' },
        { key: 'adopt', label: 'Adopt' },
      ],
    }),
    template: '<Stepper :steps="steps" current="configure" orientation="horizontal" />',
  }),
}

/** Vertical, with a done / active / todo mix and `sub` lines. */
export const VerticalMixed: Story = {
  render: () => ({
    components: { Stepper },
    setup: () => ({
      steps: [
        { key: 'stop', label: 'Stop engine', sub: 'Graceful shutdown of the running JAR' },
        { key: 'backup', label: 'Back up database', sub: 'Snapshot before the migration' },
        { key: 'swap', label: 'Swap JAR', sub: 'Install the new engine version' },
        { key: 'migrate', label: 'Migrate & boot', sub: 'Run schema migration, restart' },
      ],
    }),
    template: '<Stepper :steps="steps" current="swap" orientation="vertical" />',
  }),
}

/** Vertical at the first step — only the active dot, the rest are todo. */
export const VerticalStart: Story = {
  render: () => ({
    components: { Stepper },
    setup: () => ({
      steps: [
        { key: 'stop', label: 'Stop engine' },
        { key: 'backup', label: 'Back up database' },
        { key: 'swap', label: 'Swap JAR' },
      ],
    }),
    template: '<Stepper :steps="steps" current="stop" orientation="vertical" />',
  }),
}
