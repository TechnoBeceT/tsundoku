import type { Meta, StoryObj } from '@storybook/vue3'
import LibraryDedupDialog from './LibraryDedupDialog.vue'
// Load this screen's status tokens directly: index.css does not @import them yet,
// so the side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the library-wide duplicate-source clean-up trigger. Click the
 * button in any story to see the QCAT-222 destructive ConfirmModal it opens —
 * the sweep renames CBZ files across the whole library, so the button alone never
 * starts it. Flip the Storybook theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'Settings/LibraryDedupDialog',
  component: LibraryDedupDialog,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof LibraryDedupDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Idle — the trigger, with no outcome line yet. */
export const Default: Story = {
  args: {},
}

/** §16 in-flight — the trigger is disabled and reports that it is starting. */
export const Starting: Story = {
  args: { busy: true },
}

/** §16 success — the 202 "started" line the async sweep returns. */
export const Started: Story = {
  args: {
    message: 'Dedup started — duplicate sources merge in the background; results appear as you revisit each series',
  },
}

/** §16 error — a visible, specific failure instead of a silent no-op. */
export const Failed: Story = {
  args: { error: 'Dedup failed — the engine host is unreachable (502)' },
}
