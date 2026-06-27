import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import TextField from './TextField.vue'

/**
 * Stories for the labelled TextField. Covers labelled vs bare, a password type,
 * the monospace + disabled variants, and live v-model binding. Flip the theme to
 * confirm the focus ring + token surfaces in both modes.
 */
const meta = {
  title: 'UI/TextField',
  component: TextField,
  argTypes: {
    label: { control: 'text' },
    type: { control: 'text' },
    placeholder: { control: 'text' },
    disabled: { control: 'boolean' },
    mono: { control: 'boolean' },
  },
  args: { label: 'Display name', placeholder: 'e.g. Solo Leveling', disabled: false, mono: false },
  render: (args) => ({
    components: { TextField },
    setup: () => {
      const value = ref('')
      return { args, value }
    },
    template: '<div style="max-width:320px"><TextField v-bind="args" v-model="value" /></div>',
  }),
} satisfies Meta<typeof TextField>

export default meta
type Story = StoryObj<typeof meta>

/** Default labelled field. */
export const Default: Story = {}

/** No label — a bare input. */
export const NoLabel: Story = {
  args: { label: undefined },
}

/** Password input type. */
export const Password: Story = {
  args: { label: 'Owner password', type: 'password', placeholder: '••••••••' },
}

/** Monospace value (URLs / tokens / IDs). */
export const Mono: Story = {
  args: { label: 'FlareSolverr URL', mono: true, placeholder: 'http://localhost:8191' },
}

/** Disabled field. */
export const Disabled: Story = {
  args: { disabled: true },
  render: (args) => ({
    components: { TextField },
    setup: () => {
      const value = ref('Locked value')
      return { args, value }
    },
    template: '<div style="max-width:320px"><TextField v-bind="args" v-model="value" /></div>',
  }),
}
