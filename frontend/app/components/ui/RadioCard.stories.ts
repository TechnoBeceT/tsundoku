import type { Meta, StoryObj } from '@storybook/vue3'
import RadioCard from './RadioCard.vue'

/**
 * Stories for the RadioCard choice card. Covers selected / unselected for the
 * default (accent) variant and the danger variant, plus an interactive group
 * that behaves like the real delete-dialog choice. Each story uses the `hint`
 * slot. Flip the theme toolbar to confirm both themes read correctly.
 */
const meta = {
  title: 'UI/RadioCard',
  component: RadioCard,
  argTypes: {
    selected: { control: 'boolean' },
    variant: { control: { type: 'inline-radio' }, options: ['default', 'danger'] },
  },
  args: { selected: false, variant: 'default' },
  render: (args) => ({
    components: { RadioCard },
    setup: () => ({ args }),
    template:
      '<div style="max-width:440px"><RadioCard v-bind="args">Keep files on disk<template #hint>Removes library tracking only. Recoverable later via a library rescan.</template></RadioCard></div>',
  }),
} satisfies Meta<typeof RadioCard>

export default meta
type Story = StoryObj<typeof meta>

/** Unselected, default variant. */
export const Unselected: Story = { args: { selected: false } }

/** Selected, default (accent) variant. */
export const Selected: Story = { args: { selected: true } }

/** Danger variant, unselected. */
export const DangerUnselected: Story = {
  args: { selected: false, variant: 'danger' },
  render: (args) => ({
    components: { RadioCard },
    setup: () => ({ args }),
    template:
      '<div style="max-width:440px"><RadioCard v-bind="args">Also delete downloaded files<template #hint>Permanently removes all CBZ files from disk. This cannot be undone.</template></RadioCard></div>',
  }),
}

/** Danger variant, selected. */
export const DangerSelected: Story = {
  args: { selected: true, variant: 'danger' },
  render: (args) => ({
    components: { RadioCard },
    setup: () => ({ args }),
    template:
      '<div style="max-width:440px"><RadioCard v-bind="args">Also delete downloaded files<template #hint>Permanently removes all CBZ files from disk. This cannot be undone.</template></RadioCard></div>',
  }),
}
