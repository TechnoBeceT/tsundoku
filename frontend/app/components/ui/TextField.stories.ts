import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import TextField from './TextField.vue'

/**
 * Stories for the labelled TextField. Covers labelled vs bare, a password type
 * (with autofill metadata), the monospace + disabled variants, the compact
 * fixed-width variant, and live v-model binding. Flip the theme to confirm the
 * focus ring + token surfaces in both modes.
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
    autocomplete: { control: 'text' },
    name: { control: 'text' },
    compact: { control: 'boolean' },
  },
  args: { label: 'Display name', placeholder: 'e.g. Solo Leveling', disabled: false, mono: false, compact: false },
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

/** Password input type, with the autofill metadata the Auth form relies on. */
export const Password: Story = {
  args: {
    label: 'Owner password',
    type: 'password',
    placeholder: '••••••••',
    autocomplete: 'current-password',
    name: 'password',
  },
}

/** Compact fixed-width variant — the Settings integer rows (e.g. max retries). */
export const Compact: Story = {
  args: { label: undefined, type: 'number', compact: true, placeholder: '3' },
  render: (args) => ({
    components: { TextField },
    setup: () => {
      const value = ref('3')
      return { args, value }
    },
    template: '<TextField v-bind="args" v-model="value" />',
  }),
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
