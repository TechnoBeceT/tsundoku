import type { Meta, StoryObj } from '@storybook/vue3'
import SurfaceCard from './SurfaceCard.vue'
import LockedRow from './LockedRow.vue'
import AppButton from './AppButton.vue'

/**
 * Stories for SurfaceCard — the shared titled surface-card shell. Covers the
 * title + sub header, a title-only header, the header-right `actions` slot, a
 * header-less card (body only), and a fully custom `header` slot. Each is shown
 * inside a card-width frame; flip the theme toolbar to confirm the `--surface`
 * panel + border re-tint in both modes.
 */
const meta = {
  title: 'UI/SurfaceCard',
  component: SurfaceCard,
  argTypes: {
    title: { control: 'text' },
    sub: { control: 'text' },
  },
  args: {
    title: 'System',
    sub: 'Set at deploy time via environment variables — read-only here.',
  },
  decorators: [
    () => ({ template: '<div style="width:480px"><story /></div>' }),
  ],
  render: (args) => ({
    components: { SurfaceCard, LockedRow },
    setup: () => ({ args }),
    template:
      '<SurfaceCard v-bind="args">'
      + '<LockedRow label="Storage folder" value="/data/library" />'
      + '<LockedRow label="Server port" value="8080" />'
      + '</SurfaceCard>',
  }),
} satisfies Meta<typeof SurfaceCard>

export default meta
type Story = StoryObj<typeof meta>

/** Title + sub header over a body of locked rows. */
export const Default: Story = {}

/** Title only — no sub line. */
export const TitleOnly: Story = {
  args: { title: 'Categories', sub: undefined },
}

/** A header-right action across from the title (a CTA, a toggle, a badge). */
export const WithActions: Story = {
  render: (args) => ({
    components: { SurfaceCard, LockedRow, AppButton },
    setup: () => ({ args }),
    template:
      '<SurfaceCard v-bind="args">'
      + '<template #actions><AppButton variant="mini" size="sm">Refresh</AppButton></template>'
      + '<LockedRow label="Running version" value="v2.2.2100" />'
      + '</SurfaceCard>',
  }),
  args: { title: 'Suwayomi engine', sub: 'Tsundoku provisions and runs its own engine JAR.' },
}

/** No header at all — just the body slot inside the shell. */
export const BodyOnly: Story = {
  args: { title: undefined, sub: undefined },
  render: () => ({
    components: { SurfaceCard },
    template:
      '<SurfaceCard>'
      + '<p style="margin:0;color:var(--muted);font-size:13.5px">A bare surface panel — header omitted, body only.</p>'
      + '</SurfaceCard>',
  }),
}
