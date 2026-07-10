import type { Meta, StoryObj } from '@storybook/vue3'
import ReaderChrome from './ReaderChrome.vue'

/**
 * Stories for the reader chrome overlay. The chrome pins its bars to the top and
 * bottom of its positioned ancestor, so each story renders inside a fixed-height
 * framed viewport (matching the reader route's `.reader` container). `back` /
 * `toggle-settings` are logged via Storybook actions.
 */
const meta = {
  title: 'Reader/ReaderChrome',
  component: ReaderChrome,
  parameters: { layout: 'fullscreen' },
  decorators: [() => ({
    template: '<div style="position:relative;height:520px;background:var(--bg);border:1px solid var(--border)"><story /></div>',
  })],
  args: {
    title: 'Solo Leveling',
    chapterLabel: 'Chapter 12 · The Real Hunt Begins',
    pageLabel: '8 / 34',
    percent: 42,
  },
} satisfies Meta<typeof ReaderChrome>

export default meta
type Story = StoryObj<typeof meta>

/** Visible: both bars shown over the (empty) reader area. */
export const Visible: Story = { args: { visible: true } }

/** Hidden: the bars have slid off-screen (a centre tap brings them back). */
export const Hidden: Story = { args: { visible: false } }

/** Start of a series — near-zero progress, first chapter. */
export const StartOfSeries: Story = {
  args: { visible: true, chapterLabel: 'Chapter 1 · Prologue', pageLabel: '1 / 20', percent: 1 },
}

/** Fullscreen supported: the bottom bar shows the enter-fullscreen toggle. */
export const FullscreenAvailable: Story = {
  args: { visible: true, fullscreenSupported: true, fullscreen: false },
}

/** Currently fullscreen: the toggle shows the exit-fullscreen (minimise) icon. */
export const FullscreenActive: Story = {
  args: { visible: true, fullscreenSupported: true, fullscreen: true },
}
