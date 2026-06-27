import type { Meta, StoryObj } from '@storybook/vue3'
import AppButton from './AppButton.vue'

/**
 * Stories for the AppButton primitive. Exercises every variant, the size ladder,
 * the loading + disabled states, and the leading-icon slot. Flip the Storybook
 * theme toolbar to confirm the token-backed treatments re-tint in both themes.
 */
const meta = {
  title: 'UI/AppButton',
  component: AppButton,
  argTypes: {
    variant: { control: { type: 'select' }, options: ['primary', 'ghost', 'solid', 'mini', 'text', 'danger-ghost'] },
    size: { control: { type: 'inline-radio' }, options: ['sm', 'md', 'lg'] },
    loading: { control: 'boolean' },
    disabled: { control: 'boolean' },
  },
  args: { variant: 'primary', size: 'md', loading: false, disabled: false },
  render: (args) => ({
    components: { AppButton },
    setup: () => ({ args }),
    template: '<AppButton v-bind="args">Adopt series</AppButton>',
  }),
} satisfies Meta<typeof AppButton>

export default meta
type Story = StoryObj<typeof meta>

/** The accent-gradient primary CTA. */
export const Primary: Story = {}

/** Every variant side by side. */
export const Variants: Story = {
  render: () => ({
    components: { AppButton },
    template:
      '<div style="display:flex;flex-wrap:wrap;gap:12px;align-items:center">' +
      '<AppButton variant="primary">Primary</AppButton>' +
      '<AppButton variant="solid">Solid</AppButton>' +
      '<AppButton variant="ghost">Ghost</AppButton>' +
      '<AppButton variant="mini">Mini</AppButton>' +
      '<AppButton variant="text">Text</AppButton>' +
      '<AppButton variant="danger-ghost">Danger</AppButton>' +
      '</div>',
  }),
}

/** The three sizes of the primary button. */
export const Sizes: Story = {
  render: () => ({
    components: { AppButton },
    template:
      '<div style="display:flex;gap:12px;align-items:center">' +
      '<AppButton size="sm">Small</AppButton>' +
      '<AppButton size="md">Medium</AppButton>' +
      '<AppButton size="lg">Large</AppButton>' +
      '</div>',
  }),
}

/** Loading swaps the leading icon for a spinner and disables the button. */
export const Loading: Story = {
  render: () => ({
    components: { AppButton },
    template:
      '<div style="display:flex;gap:12px;align-items:center">' +
      '<AppButton variant="primary" loading>Saving</AppButton>' +
      '<AppButton variant="ghost" loading>Saving</AppButton>' +
      '<AppButton variant="danger-ghost" loading>Removing</AppButton>' +
      '</div>',
  }),
}

/** Disabled treatment per variant. */
export const Disabled: Story = {
  args: { disabled: true },
}

/** With a leading icon in the `icon` slot. */
export const WithIcon: Story = {
  render: () => ({
    components: { AppButton },
    template:
      '<AppButton variant="primary">' +
      '<template #icon><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14M5 12h14"/></svg></template>' +
      'Adopt series</AppButton>',
  }),
}
