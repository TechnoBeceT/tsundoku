import type { Meta, StoryObj } from '@storybook/vue3'
import ReaderPage from './ReaderPage.vue'

/**
 * Stories for one reader page. The strip stacks these into the vertical long-strip.
 * A reader column is narrow, so each story renders inside a ~480px frame. Flip the
 * Storybook theme toolbar to confirm the reserved/failed tiles work in both themes.
 */
const meta = {
  title: 'Reader/ReaderPage',
  component: ReaderPage,
  parameters: { layout: 'centered' },
  decorators: [() => ({ template: '<div style="width:480px"><story /></div>' })],
} satisfies Meta<typeof ReaderPage>

export default meta
type Story = StoryObj<typeof meta>

/** Loaded: a real portrait page image fills the column at its natural height. */
export const Loaded: Story = {
  args: { src: 'https://picsum.photos/seed/reader-page/800/1200', alt: 'Page 1' },
}

/** Loading: no src yet — the box reserves its aspect height so the strip does not jump. */
export const Loading: Story = {
  args: { src: '', alt: 'Page 1' },
}

/** Failed: a broken URL triggers the "page unavailable" placeholder (drives the tail-404 tolerance). */
export const Failed: Story = {
  args: { src: 'https://example.invalid/missing.png', alt: 'Page 99' },
}
