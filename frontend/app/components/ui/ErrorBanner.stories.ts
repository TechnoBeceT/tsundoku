import type { Meta, StoryObj } from '@storybook/vue3'
import ErrorBanner from './ErrorBanner.vue'

/**
 * Stories for the dismissible ErrorBanner. Covers the dismissible default and the
 * non-dismissible variant. Flip the theme to confirm the danger tokens read in
 * both modes.
 */
const meta = {
  title: 'UI/ErrorBanner',
  component: ErrorBanner,
  argTypes: {
    message: { control: 'text' },
    dismissible: { control: 'boolean' },
  },
  args: {
    message: 'Could not reach Suwayomi — check the engine is running.',
    dismissible: true,
  },
  render: (args) => ({
    components: { ErrorBanner },
    setup: () => ({ args }),
    template: '<div style="max-width:460px"><ErrorBanner v-bind="args" /></div>',
  }),
} satisfies Meta<typeof ErrorBanner>

export default meta
type Story = StoryObj<typeof meta>

/** Default dismissible banner. */
export const Default: Story = {}

/** Non-dismissible (no close button). */
export const NonDismissible: Story = {
  args: { dismissible: false },
}
