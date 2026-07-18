import type { Meta, StoryObj } from '@storybook/vue3'
import NetworkEndpointDialog from './NetworkEndpointDialog.vue'
import type { NetworkEndpoint } from '../screens/settings.types'
import '../../assets/css/tokens/settings.css'

const socksEndpoint: NetworkEndpoint = {
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

/**
 * Stories for the endpoint add/edit dialog. Covers adding a SOCKS endpoint,
 * editing an existing one (the write-only password field opens BLANK with its
 * "unchanged" placeholder), an inline backend save error, and the §16 busy state.
 */
const meta = {
  title: 'Settings/NetworkEndpointDialog',
  component: NetworkEndpointDialog,
  parameters: { layout: 'centered' },
  args: { open: true, endpoint: null, busy: false, error: null },
} satisfies Meta<typeof NetworkEndpointDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Add — a blank SOCKS form (the default endpoint type). */
export const AddSocks: Story = {}

/** Edit — an existing SOCKS endpoint; the password field is blank (write-only). */
export const EditSocks: Story = {
  args: { endpoint: socksEndpoint },
}

/** §16: a backend save rejection surfaces inline inside the dialog. */
export const SaveError: Story = {
  args: { endpoint: socksEndpoint, error: 'socks host must not be blank' },
}

/** §16 busy: the form is disabled + the Save button spins mid-save. */
export const Busy: Story = {
  args: { endpoint: socksEndpoint, busy: true },
}
