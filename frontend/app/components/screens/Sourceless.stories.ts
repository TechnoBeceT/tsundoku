import type { Meta, StoryObj } from '@storybook/vue3'
import Sourceless from './Sourceless.vue'

/**
 * Stories for the library-wide Sourceless screen. Unlike the other library
 * screens (props-down, e.g. `Fractionals`), `Sourceless.vue` is self-contained
 * — it owns `useSourceless()` and the reused `SourcelessCleanupDialog` directly
 * (see its doc comment), so there is no `series`/`pending` arg to drive states
 * from here. The repo has no MSW/mock-server addon for Storybook, so this
 * story renders the real component as-is; in a served preview with no backend
 * behind it, `useSourceless()`'s load fails gracefully into its own error +
 * empty-grid state (the same §16 failure path a real network outage takes),
 * rather than crashing. Flip the theme toolbar to confirm both themes read.
 */
const meta = {
  title: 'Screens/Sourceless',
  component: Sourceless,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof Sourceless>

export default meta
type Story = StoryObj<typeof meta>

/** The screen as mounted by the page — see the component doc for why there are no args. */
export const Default: Story = {
  args: {},
}
