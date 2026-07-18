import type { Meta, StoryObj } from '@storybook/vue3'
import NetworkEndpointRow from './NetworkEndpointRow.vue'
import type { NetworkEndpoint } from '../screens/settings.types'
import '../../assets/css/tokens/settings.css'

const socks: NetworkEndpoint = {
  id: 'ep-vpn-socks',
  name: 'VPN SOCKS',
  kind: 'socks',
  enabled: true,
  host: '10.0.1.9',
  port: 1080,
  socksVersion: 5,
  username: 'tsundoku',
  url: '',
  session: '',
  sessionTtl: 0,
  timeout: 0,
  asResponseFallback: true,
}

const flare: NetworkEndpoint = {
  id: 'ep-vpn-flare',
  name: 'VPN FlareSolverr',
  kind: 'flaresolverr',
  enabled: true,
  host: '',
  port: 0,
  socksVersion: 5,
  username: '',
  url: 'http://flaresolverr-vpn:8191',
  session: 'sess-a',
  sessionTtl: 15,
  timeout: 60,
  asResponseFallback: false,
}

/**
 * Stories for one endpoint row. Covers the SOCKS + FlareSolverr variants, the
 * disabled tag, and the §16 busy (mid-mutation) state.
 */
const meta = {
  title: 'Settings/NetworkEndpointRow',
  component: NetworkEndpointRow,
  parameters: { layout: 'padded' },
  args: { endpoint: socks, busy: false },
} satisfies Meta<typeof NetworkEndpointRow>

export default meta
type Story = StoryObj<typeof meta>

/** A SOCKS proxy endpoint (host:port + version summary). */
export const Socks: Story = {}

/** A FlareSolverr endpoint (URL summary). */
export const FlareSolverr: Story = {
  args: { endpoint: flare },
}

/** A disabled endpoint carries the muted "Disabled" tag. */
export const Disabled: Story = {
  args: { endpoint: { ...socks, enabled: false } },
}

/** §16 busy: the row dims + spins while its mutation runs. */
export const Busy: Story = {
  args: { busy: true },
}
