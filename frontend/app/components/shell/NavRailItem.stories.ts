import type { Meta, StoryObj } from '@storybook/vue3'
import NavRailItem from './NavRailItem.vue'

/**
 * Stories for the nav-rail icon button. The item is designed to sit on the rail
 * surface, so each story frames it on a `var(--rail)` tile (the badge's border is
 * `var(--rail)`, so it only reads correctly against that backdrop). Flip the
 * Storybook theme toolbar to confirm the active tint + badges read in both themes.
 */
const meta = {
  title: 'Shell/NavRailItem',
  component: NavRailItem,
  // Frame every story on the rail surface so the badge border + active tint read.
  decorators: [() => ({
    template: '<div style="display:inline-flex;padding:14px;border-radius:var(--radius-xl);background:var(--rail)"><story /></div>',
  })],
  argTypes: {
    icon: { control: 'text' },
    label: { control: 'text' },
    active: { control: 'boolean' },
  },
  args: { icon: 'book', label: 'Library', active: false },
} satisfies Meta<typeof NavRailItem>

export default meta
type Story = StoryObj<typeof meta>

/** Resting (inactive) item. */
export const Inactive: Story = {}

/** Active item — accent tint + `aria-current="page"`. */
export const Active: Story = {
  args: { active: true },
}

/** With a danger (rose) count badge — e.g. unhealthy sources. */
export const WithBadge: Story = {
  args: { icon: 'activity', label: 'Library Health', badge: { count: 3, tone: 'danger' } },
}

/** With a warn (amber) count badge — e.g. failed downloads. */
export const WithWarnBadge: Story = {
  args: { icon: 'download', label: 'Downloads', badge: { count: 1, tone: 'warn' } },
}

/** The whole rail at a glance: active item, a pinned-style item, and both badges. */
export const All: Story = {
  render: () => ({
    components: { NavRailItem },
    template:
      '<div style="display:inline-flex;flex-direction:column;gap:9px;padding:14px;border-radius:var(--radius-xl);background:var(--rail)">' +
      '<NavRailItem icon="book" label="Library" :active="true" />' +
      '<NavRailItem icon="compass" label="Discover" />' +
      '<NavRailItem icon="download" label="Downloads" :badge="{ count: 1, tone: \'warn\' }" />' +
      '<NavRailItem icon="activity" label="Library Health" :badge="{ count: 3, tone: \'danger\' }" />' +
      '<NavRailItem icon="settings" label="Settings" />' +
      '</div>',
  }),
}
