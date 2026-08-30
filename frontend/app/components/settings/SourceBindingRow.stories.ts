import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, within } from 'storybook/test'
import SourceBindingRow from './SourceBindingRow.vue'
import { networkEndpoints } from '../../fixtures/settings'
import type { NetworkSource, SourceBinding } from '../screens/settings.types'
import '../../assets/css/tokens/settings.css'

const source: NetworkSource = { id: '9127482910938471028', name: 'Source A', lang: 'en' }
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

/** Stored routes remain visible and clearable when one endpoint is missing and the other is disabled. */
export const UnavailableBindings: Story = {
  parameters: { viewport: { defaultViewport: 'mobile1' } },
  args: {
    binding: {
      ...boundBinding,
      socksEndpointId: 'missing-socks-endpoint',
    },
    flareEndpoints: flareEndpoints.map(endpoint => ({ ...endpoint, enabled: false })),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByRole('combobox', { name: `SOCKS route for ${source.name}` })).toHaveValue('missing-socks-endpoint')
    await expect(canvas.getByRole('option', { name: 'Missing endpoint (missing-socks-endpoint)' })).toBeVisible()
    await expect(canvas.getByRole('option', { name: 'VPN FlareSolverr (disabled)' })).toBeVisible()
    await expect(canvas.getByRole('button', { name: 'Use global default' })).toBeEnabled()
  },
}

/** §16 busy: the row dims + spins while its set/clear mutation runs. */
export const Busy: Story = {
  args: { binding: boundBinding, busy: true },
}
