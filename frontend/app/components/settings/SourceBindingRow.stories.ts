import type { Meta, StoryObj } from '@storybook/vue3'
import SourceBindingRow from './SourceBindingRow.vue'
import { networkEndpoints } from '../../fixtures/settings'
import type { NetworkSource, SourceBinding } from '../screens/settings.types'
import '../../assets/css/tokens/settings.css'

const source: NetworkSource = { id: '9127482910938471028', name: 'Omega Scans', lang: 'en' }
const socksEndpoints = networkEndpoints.filter(ep => ep.kind === 'socks')
const flareEndpoints = networkEndpoints.filter(ep => ep.kind === 'flaresolverr')

const boundBinding: SourceBinding = {
  sourceId: source.id,
  socksEndpointId: 'ep-vpn-socks',
  flareMode: 'endpoint',
  flareEndpointId: 'ep-vpn-flare',
}

/**
 * Stories for one per-source assignment row. Covers an unbound source (both
 * selects on the global default, Clear disabled), a bound source (routed through
 * both VPN endpoints, accent rule + Clear enabled), and the §16 busy state.
 */
const meta = {
  title: 'Settings/SourceBindingRow',
  component: SourceBindingRow,
  parameters: { layout: 'padded' },
  args: { source, binding: null, socksEndpoints, flareEndpoints, busy: false },
} satisfies Meta<typeof SourceBindingRow>

export default meta
type Story = StoryObj<typeof meta>

/** Unbound — uses the global default; the Clear button is disabled. */
export const Unbound: Story = {}

/** Bound — routed through both VPN endpoints (accent left rule + Clear enabled). */
export const Bound: Story = {
  args: { binding: boundBinding },
}

/** §16 busy: the row dims + spins while its set/clear mutation runs. */
export const Busy: Story = {
  args: { binding: boundBinding, busy: true },
}
