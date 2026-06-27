import type { Meta, StoryObj } from '@storybook/vue3'
import RequeueConfirmModal from './RequeueConfirmModal.vue'

/**
 * Stories for RequeueConfirmModal — the bulk retry/reset confirmation. The copy
 * pluralises from `count`; flip the theme toolbar to confirm the dialog chrome
 * reads on both surfaces. Rendered fullscreen so the overlay fills the canvas.
 */
const meta = {
  title: 'Downloads/RequeueConfirmModal',
  component: RequeueConfirmModal,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof RequeueConfirmModal>

export default meta
type Story = StoryObj<typeof meta>

/** Open, confirming a multi-chapter requeue. */
export const Open: Story = {
  args: { open: true, count: 7 },
}

/** Open with a single chapter — the copy reads "1 chapter" (no plural). */
export const SingleChapter: Story = {
  args: { open: true, count: 1 },
}
