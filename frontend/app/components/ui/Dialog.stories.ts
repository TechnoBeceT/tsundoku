import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import Dialog from './Dialog.vue'
import AppButton from './AppButton.vue'

/**
 * Stories for the reka-ui-backed Dialog shell. The stories drive `:open=true` so
 * the portaled card is visible on load. NOTE: the portaled content can paint
 * ~1.5s after mount in a headless render — if a smoke check can't find the panel
 * instantly, that's a timing artifact, not a render bug. Flip the theme to
 * confirm the overlay + card tokens in both modes.
 */
const meta = {
  title: 'UI/Dialog',
  component: Dialog,
  args: { open: true, title: 'Remove source', busy: false },
} satisfies Meta<typeof Dialog>

export default meta
type Story = StoryObj<typeof meta>

/** Open dialog with a body + an actions footer. */
export const Open: Story = {
  render: (args) => ({
    components: { Dialog, AppButton },
    setup: () => {
      const open = ref(true)
      return { args, open }
    },
    template:
      '<div style="min-height:340px">' +
      '<Dialog v-bind="args" v-model:open="open">' +
      'Removing this source deletes its chapter feed but keeps every downloaded file on disk.' +
      '<template #actions>' +
      '<AppButton variant="ghost" @click="open = false">Cancel</AppButton>' +
      '<AppButton variant="danger-ghost" @click="open = false">Remove</AppButton>' +
      '</template>' +
      '</Dialog>' +
      '</div>',
  }),
}

/** Busy state — the close button + Escape/overlay dismissal are suppressed. */
export const Busy: Story = {
  args: { busy: true },
  render: (args) => ({
    components: { Dialog, AppButton },
    setup: () => {
      const open = ref(true)
      return { args, open }
    },
    template:
      '<div style="min-height:340px">' +
      '<Dialog v-bind="args" v-model:open="open">' +
      'Saving your changes…' +
      '<template #actions>' +
      '<AppButton variant="primary" loading>Removing</AppButton>' +
      '</template>' +
      '</Dialog>' +
      '</div>',
  }),
}
