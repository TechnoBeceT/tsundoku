import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, within } from 'storybook/test'
import ResponsiveGrid from './ResponsiveGrid.vue'

/**
 * Stories for ResponsiveGrid — the one fluid card-grid primitive (QCAT-259). The
 * four presets below mirror the real library grids' props (Library / Discover /
 * Categories / Library Health), and `PhoneNoOverflow` frames the grid at a 360px
 * phone width and asserts ZERO horizontal overflow (QCAT-230).
 *
 * Flip the Storybook viewport toolbar to watch the fluid `rem` floor scale the
 * tile minimum with the root font-size, and the column count reflow — then, at
 * the phone viewports (≤430px), watch the count STOP reflowing and the tiles grow
 * instead: that is `phone-columns` (QCAT-263). Switching "Small mobile" (320px) →
 * "Large mobile" (414px) must show the SAME number of tiles, BIGGER.
 *
 * ⚠️ The phone hold is driven by a VIEWPORT media query, so it engages with the
 * Storybook viewport toolbar — NOT by framing a story in a 360px-wide div. The
 * `PhoneNoOverflow` proof below is width-framed deliberately (it tests the track
 * template's shrink behaviour, which is container-driven and holds either way).
 */
const meta = {
  title: 'UI/ResponsiveGrid',
  component: ResponsiveGrid,
  parameters: { layout: 'fullscreen' },
  args: { minTile: '186px', gap: 'var(--space-xl)', fill: 'auto-fill' },
} satisfies Meta<typeof ResponsiveGrid>

export default meta
type Story = StoryObj<typeof meta>

/** A pool of placeholder tiles so the reflow is visible; `n` sets the count. */
function tiles(n: number): string {
  return Array.from({ length: n }, (_, i) => i + 1)
    .map(
      (i) =>
        `<div style="aspect-ratio:0.7;border-radius:var(--radius-xl);border:1px solid var(--border);background:var(--surface2);display:flex;align-items:center;justify-content:center;color:var(--muted);font-size:var(--text-sm)">${i}</div>`,
    )
    .join('')
}

/** Renders the grid with `count` tiles inside a padded frame. */
function grid(count: number): StoryObj<typeof meta>['render'] {
  return (args) => ({
    components: { ResponsiveGrid },
    setup: () => ({ args, inner: tiles(count) }),
    template: `<div style="padding:24px;background:var(--bg)"><ResponsiveGrid v-bind="args"><template #default><div v-html="inner" style="display:contents" /></template></ResponsiveGrid></div>`,
  })
}

/** Library preset: 186px→112px auto-fill, --space-xl→--space-sm gap, 3 HELD phone columns. */
export const Library: Story = {
  args: {
    minTile: '186px',
    gap: 'var(--space-xl)',
    fill: 'auto-fill',
    mobileMinTile: '112px',
    mobileGap: 'var(--space-sm)',
    phoneColumns: 3,
  },
  render: grid(12),
}

/** Discover preset: same cover shape as the library — 184px→112px, 3 HELD phone columns. */
export const DiscoverWithMobileOverride: Story = {
  args: {
    minTile: '184px',
    gap: 'var(--space-xl)',
    fill: 'auto-fill',
    mobileMinTile: '112px',
    mobileGap: 'var(--space-sm)',
    phoneColumns: 3,
  },
  render: grid(14),
}

/** Categories preset: 240px AUTO-FIT (empty tracks collapse so tiles stretch), 2 HELD phone columns. */
export const CategoriesAutoFit: Story = {
  args: {
    minTile: '240px',
    gap: 'var(--space-lg)',
    fill: 'auto-fit',
    mobileMinTile: '150px',
    mobileGap: 'var(--space-md)',
    phoneColumns: 2,
  },
  render: grid(5),
}

/** Library Health preset: a wide 300px→260px auto-fill floor, 1 HELD phone column. */
export const LibraryHealthWide: Story = {
  args: {
    minTile: '300px',
    gap: 'var(--space-base)',
    fill: 'auto-fill',
    mobileMinTile: '260px',
    phoneColumns: 1,
  },
  render: grid(6),
}

/**
 * The QCAT-263 phone mode, isolated: view this at the "Small mobile" (320px) and
 * "Large mobile" (414px) viewports — the tile COUNT is identical (3) and only the
 * tile SIZE changes. Compare against `LibraryNoPhoneHold` below, which is the same
 * grid without the hold and gains a 4th, narrower column instead. This is the
 * exact comparison the owner made when they rejected the auto-fill phone
 * behaviour.
 */
export const PhoneHeldColumns: Story = {
  args: {
    minTile: '186px',
    gap: 'var(--space-xl)',
    fill: 'auto-fill',
    mobileMinTile: '112px',
    mobileGap: 'var(--space-sm)',
    phoneColumns: 3,
  },
  render: grid(9),
}

/** The rejected behaviour, kept as the visual control for `PhoneHeldColumns`. */
export const LibraryNoPhoneHold: Story = {
  args: {
    minTile: '186px',
    gap: 'var(--space-xl)',
    fill: 'auto-fill',
    mobileMinTile: '112px',
    mobileGap: 'var(--space-sm)',
  },
  render: grid(9),
}

/**
 * QCAT-230 proof: framed at a 360px phone width, the grid must never overflow
 * horizontally (an overflow-x kills vertical page scroll on a phone). The play
 * function asserts the grid's `scrollWidth <= clientWidth`.
 */
export const PhoneNoOverflow: Story = {
  args: {
    minTile: '184px',
    gap: 'var(--space-xl)',
    fill: 'auto-fill',
    mobileMinTile: '132px',
    mobileGap: 'var(--space-sm)',
  },
  render: (args) => ({
    components: { ResponsiveGrid },
    setup: () => ({ args, inner: tiles(8) }),
    template: `<div style="width:360px;padding:16px;background:var(--bg)"><ResponsiveGrid v-bind="args" data-test="grid"><template #default><div v-html="inner" style="display:contents" /></template></ResponsiveGrid></div>`,
  }),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const el = canvas.getByTestId('grid')
    // No horizontal overflow: the grid never scrolls wider than its box.
    await expect(el.scrollWidth).toBeLessThanOrEqual(el.clientWidth)
  },
}
