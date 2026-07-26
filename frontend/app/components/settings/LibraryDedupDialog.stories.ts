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
    message: 'Dedup started — duplicate sources merge in the background; the result appears here when it finishes',
  },
}

/**
 * §16 completion — the terminal summary that arrives on the `library.dedup.done`
 * SSE event once the detached sweep lands. This is what the owner actually reads;
 * the "started" line above is only the halfway state.
 */
export const Finished: Story = {
  args: {
    message: 'Clean-up finished — merged 4 duplicate sources across 128 series',
  },
}

/**
 * Series were skipped because a merge was already running on them (an in-flight
 * match/consolidation, or the automatic self-heal). Amber, not rose: nothing
 * failed, there is just work left — and the line says exactly what to do about it.
 */
export const SkippedBusySeries: Story = {
  args: {
    message: 'Clean-up finished — merged 4 duplicate sources across 126 series',
    skippedBusy: 3,
  },
}

/** The singular wording, so "1 series was busy and was skipped" reads correctly. */
export const SkippedOneBusySeries: Story = {
  args: {
    message: 'Clean-up finished — no duplicate sources found across 127 series',
    skippedBusy: 1,
  },
}

/** §16 error — a visible, specific failure instead of a silent no-op. */
export const Failed: Story = {
  args: { error: 'Dedup failed — the engine host is unreachable (502)' },
}
