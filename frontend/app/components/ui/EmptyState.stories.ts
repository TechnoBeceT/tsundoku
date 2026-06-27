import type { Meta, StoryObj } from '@storybook/vue3'
import EmptyState from './EmptyState.vue'
import BrandMark from './BrandMark.vue'
import AppButton from './AppButton.vue'

/**
 * Stories for the EmptyState placeholder block. Covers a branded mark in the
 * icon disc, a lucide `<Icon>` with a tinted tone, the title-only (no `sub`)
 * variant, and an action in the default slot. Flip the theme toolbar to confirm
 * both themes.
 */
const meta = {
  title: 'UI/EmptyState',
  component: EmptyState,
} satisfies Meta<typeof EmptyState>

export default meta
type Story = StoryObj<typeof meta>

/** Branded mark in the disc, with a sub line (the empty-library look). */
export const WithBrandMark: Story = {
  render: () => ({
    components: { EmptyState, BrandMark },
    template: `
      <EmptyState title="No series yet" sub="Adopt a series from Discover to start your library.">
        <template #icon><BrandMark :size="30" tone="gradient" /></template>
      </EmptyState>
    `,
  }),
}

/** A lucide icon tinted with an accent tone, plus a sub line. */
export const WithLucideIcon: Story = {
  render: () => ({
    components: { EmptyState },
    template: `
      <EmptyState title="All clear" sub="Every source is healthy. Nothing needs your attention." icon-tone="set-ok-dot">
        <template #icon><Icon name="lucide:check" size="26" /></template>
      </EmptyState>
    `,
  }),
}

/** Title only — no `sub`, default muted icon tone. */
export const TitleOnly: Story = {
  render: () => ({
    components: { EmptyState },
    template: `
      <EmptyState title="This source returned nothing for this listing.">
        <template #icon><Icon name="lucide:compass" size="26" /></template>
      </EmptyState>
    `,
  }),
}

/** With an action in the default slot (e.g. a CTA back to Discover). */
export const WithAction: Story = {
  render: () => ({
    components: { EmptyState, AppButton },
    template: `
      <EmptyState title="No categories defined yet" sub="Create a category to organise your library." icon-tone="accentBright">
        <template #icon><Icon name="lucide:folder-plus" size="26" /></template>
        <AppButton size="sm">New category</AppButton>
      </EmptyState>
    `,
  }),
}
