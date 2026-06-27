import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import ConfirmModal from './ConfirmModal.vue'

/**
 * Stories for the ConfirmModal (a thin Dialog wrapper). The stories drive
 * `:open=true` so the portaled card is visible on load (see the Dialog story note
 * about the ~1.5s headless paint delay). Covers the default confirm, the
 * destructive variant, and a body-slot example. Flip the theme to verify both modes.
 */
const meta = {
  title: 'UI/ConfirmModal',
  component: ConfirmModal,
  args: {
    open: true,
    title: 'Requeue chapter?',
    message: 'This resets the chapter to wanted and re-runs the download.',
    confirmLabel: 'Requeue',
    cancelLabel: 'Cancel',
    destructive: false,
    busy: false,
  },
} satisfies Meta<typeof ConfirmModal>

export default meta
type Story = StoryObj<typeof meta>

/** Default (non-destructive) confirm. */
export const Default: Story = {
  render: (args) => ({
    components: { ConfirmModal },
    setup: () => {
      const open = ref(true)
      return { args, open }
    },
    template: '<div style="min-height:300px"><ConfirmModal v-bind="args" v-model:open="open" /></div>',
  }),
}

/** Destructive confirm — the confirm button is the danger treatment. */
export const Destructive: Story = {
  args: {
    title: 'Delete series?',
    message: 'This removes every chapter and downloaded file. This cannot be undone.',
    confirmLabel: 'Delete',
    destructive: true,
  },
  render: (args) => ({
    components: { ConfirmModal },
    setup: () => {
      const open = ref(true)
      return { args, open }
    },
    template: '<div style="min-height:300px"><ConfirmModal v-bind="args" v-model:open="open" /></div>',
  }),
}

/** Busy — the confirm button spins and dismissal is blocked. */
export const Busy: Story = {
  args: { busy: true },
  render: (args) => ({
    components: { ConfirmModal },
    setup: () => {
      const open = ref(true)
      return { args, open }
    },
    template: '<div style="min-height:300px"><ConfirmModal v-bind="args" v-model:open="open" /></div>',
  }),
}
