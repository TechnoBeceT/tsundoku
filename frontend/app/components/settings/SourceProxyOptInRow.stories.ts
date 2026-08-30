import type { Meta, StoryObj } from '@storybook/vue3'
import SourceProxyOptInRow from './SourceProxyOptInRow.vue'
import '../../assets/css/tokens/settings.css'

/**
 * Image proxy membership is deliberately not presented as inheritance. It is a
 * source-level safety decision with explicit On/Off wording in every state.
 */
const meta = {
  title: 'Settings/SourceProxyOptInRow',
  component: SourceProxyOptInRow,
  parameters: { layout: 'padded' },
  decorators: [
    () => ({ template: '<div style="width:min(100%,760px)"><story /></div>' }),
  ],
  args: {
    enabled: false,
    effectiveAvailable: false,
    saving: false,
    error: null,
  },
} satisfies Meta<typeof SourceProxyOptInRow>

export default meta
type Story = StoryObj<typeof meta>

/** Off keeps the source on its extension-native image path. */
export const Off: Story = {}

/** On is explicit and reports that the proxy path is currently available. */
export const On: Story = {
  args: { enabled: true, effectiveAvailable: true },
}

/** The switch is row-locally disabled while membership is being saved. */
export const Saving: Story = {
  args: { enabled: true, effectiveAvailable: true, saving: true },
}

/** A failed change leaves the confirmed Off state visible beside the error. */
export const RowLocalError: Story = {
  args: { enabled: false, effectiveAvailable: false, error: 'The image proxy selection could not be saved.' },
}

/** Opted in, but the proxy service is not currently usable. */
export const OnButUnavailable: Story = {
  args: { enabled: true, effectiveAvailable: false },
}
