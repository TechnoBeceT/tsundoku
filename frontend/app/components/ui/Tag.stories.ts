import type { Meta, StoryObj } from '@storybook/vue3'
import Tag from './Tag.vue'

/**
 * Stories for the Tag status marker. Every tone is shown with a representative
 * marker word; the on-cover tones (`success`, `frost`) are also shown over a
 * dark cover tile. Flip the Storybook theme toolbar to confirm the tones hold
 * up in both themes.
 */
const meta = {
  title: 'UI/Tag',
  component: Tag,
  argTypes: {
    tone: {
      control: { type: 'inline-radio' },
      options: ['neutral', 'accent', 'success', 'warn', 'danger', 'frost'],
    },
  },
  args: { tone: 'neutral', default: 'PLANNED' },
} satisfies Meta<typeof Tag>

export default meta
type Story = StoryObj<typeof meta>

/** Neutral marker (PLANNED / DEFAULT). */
export const Neutral: Story = {
  render: (args) => ({
    components: { Tag },
    setup: () => ({ args }),
    template: '<Tag v-bind="args">PLANNED</Tag>',
  }),
}

/** Accent marker (PREFERRED). */
export const Accent: Story = {
  args: { tone: 'accent' },
  render: (args) => ({
    components: { Tag },
    setup: () => ({ args }),
    template: '<Tag v-bind="args">PREFERRED</Tag>',
  }),
}

/** Success marker (DONE / IN LIBRARY) with a check icon. */
export const Success: Story = {
  args: { tone: 'success' },
  render: (args) => ({
    components: { Tag },
    setup: () => ({ args }),
    template:
      '<Tag v-bind="args"><template #icon><svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5" /></svg></template>DONE</Tag>',
  }),
}

/** Warn marker (UPGRADE / UPDATE). */
export const Warn: Story = {
  args: { tone: 'warn' },
  render: (args) => ({
    components: { Tag },
    setup: () => ({ args }),
    template:
      '<Tag v-bind="args"><template #icon><svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7" /></svg></template>UPGRADE</Tag>',
  }),
}

/** Danger marker. */
export const Danger: Story = {
  args: { tone: 'danger' },
  render: (args) => ({
    components: { Tag },
    setup: () => ({ args }),
    template: '<Tag v-bind="args">ERROR</Tag>',
  }),
}

/** Frost marker (PAUSED) over a dark cover tile (its intended placement). */
export const Frost: Story = {
  args: { tone: 'frost' },
  render: (args) => ({
    components: { Tag },
    setup: () => ({ args }),
    template:
      '<div style="display:inline-flex;padding:24px;border-radius:var(--radius-xl);background:var(--cover-placeholder)"><Tag v-bind="args"><template #icon><svg width="9" height="9" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="5" width="4" height="14" rx="1" /><rect x="14" y="5" width="4" height="14" rx="1" /></svg></template>PAUSED</Tag></div>',
  }),
}

/** All tones side by side for a quick visual diff. */
export const AllTones: Story = {
  render: () => ({
    components: { Tag },
    template:
      '<div style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">' +
      '<Tag tone="neutral">PLANNED</Tag>' +
      '<Tag tone="accent">PREFERRED</Tag>' +
      '<Tag tone="success">DONE</Tag>' +
      '<Tag tone="warn">UPGRADE</Tag>' +
      '<Tag tone="danger">ERROR</Tag>' +
      '<div style="display:inline-flex;padding:10px;border-radius:var(--radius-lg);background:var(--cover-placeholder)"><Tag tone="frost">PAUSED</Tag></div>' +
      '</div>',
  }),
}
