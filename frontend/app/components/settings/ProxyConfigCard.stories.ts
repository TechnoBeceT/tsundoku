import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import ProxyConfigCard from './ProxyConfigCard.vue'
import { suwayomiConfig } from '../../fixtures/settings'
import type { SocksProxyConfig } from '../screens/settings.types'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the toggle-gated SOCKS-proxy card. The wrapper holds a live model
 * so the enable toggle reveals/hides the fields. Flip the theme toolbar for both.
 */
const meta = {
  title: 'Settings/ProxyConfigCard',
  component: ProxyConfigCard,
  parameters: { layout: 'padded' },
  // modelValue is a required prop; each story renders its own live-model wrapper,
  // so this default only satisfies the CSF3 story typing.
  args: { modelValue: suwayomiConfig.socks },
} satisfies Meta<typeof ProxyConfigCard>

export default meta
type Story = StoryObj<typeof meta>

// A live-model wrapper so the toggle + fields are interactive in the story.
const withModel = (seed: SocksProxyConfig) => ({
  components: { ProxyConfigCard },
  setup() {
    const model = ref<SocksProxyConfig>({ ...seed })
    return { model }
  },
  template: `<ProxyConfigCard v-model="model" />`,
})

/** Disabled — only the header + toggle show (the seed has SOCKS off). */
export const Off: Story = {
  render: () => withModel(suwayomiConfig.socks),
}

/** Enabled — the connection fields are revealed. */
export const On: Story = {
  render: () => withModel({ ...suwayomiConfig.socks, enabled: true, host: '127.0.0.1' }),
}
