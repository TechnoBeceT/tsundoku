import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import Auth from './Auth.vue'

/**
 * Stories for the pre-boot auth screen — the bare, full-screen card shown before
 * the app shell. Flip the Storybook theme toolbar to confirm the radial backdrop
 * and card read correctly in BOTH dark and light. `Claim` and `Login` are
 * interactive (the mode-switch link flips between them); `LoginError` shows a
 * surfaced server error; `Submitting` shows the in-flight button state.
 */
const meta = {
  title: 'Screens/Auth',
  component: Auth,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof Auth>

export default meta
type Story = StoryObj<typeof meta>

/**
 * First-run owner creation. The mode-switch link is wired to local state so it
 * actually flips to login (and back), demonstrating the single-screen toggle.
 */
export const Claim: Story = {
  render: () => ({
    components: { Auth },
    setup() {
      const mode = ref<'claim' | 'login'>('claim')
      const onSwitch = (): void => {
        mode.value = mode.value === 'claim' ? 'login' : 'claim'
      }
      return { mode, onSwitch }
    },
    template: `<Auth :mode="mode" @switch-mode="onSwitch" />`,
  }),
}

/** Returning-owner login (the default repeat-visit state). */
export const Login: Story = {
  render: () => ({
    components: { Auth },
    setup() {
      const mode = ref<'claim' | 'login'>('login')
      const onSwitch = (): void => {
        mode.value = mode.value === 'claim' ? 'login' : 'claim'
      }
      return { mode, onSwitch }
    },
    template: `<Auth :mode="mode" @switch-mode="onSwitch" />`,
  }),
}

/** Login with a surfaced server error — the generic 401 "Invalid credentials". */
export const LoginError: Story = {
  args: {
    mode: 'login',
    error: 'Invalid credentials',
  },
}

/** Submit in flight — the button is disabled and shows its spinner (§16 loading). */
export const Submitting: Story = {
  args: {
    mode: 'login',
    loading: true,
  },
}
