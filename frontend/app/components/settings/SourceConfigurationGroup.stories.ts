import type { Meta, StoryObj } from '@storybook/vue3'
import SourceConfigurationGroup from './SourceConfigurationGroup.vue'
import {
  fullyInheritedSourceConfiguration,
  networkEndpoints,
} from '../../fixtures/settings'
import '../../assets/css/tokens/settings.css'

/** The focused source editor, including inherited, effective, and runtime apply state. */
const meta = {
  title: 'Settings/SourceConfigurationGroup',
  component: SourceConfigurationGroup,
  parameters: { layout: 'padded' },
  decorators: [
    () => ({ template: '<div style="width:min(100%,900px)"><story /></div>' }),
  ],
  args: {
    configuration: {
      ...fullyInheritedSourceConfiguration,
      kcef: {
        override: 'required',
        global: 'auto',
        effective: 'required',
        inherited: false,
        enabled: true,
      },
    },
    endpoints: networkEndpoints,
  },
} satisfies Meta<typeof SourceConfigurationGroup>

export default meta
type Story = StoryObj<typeof meta>

/** An explicit Required policy shows that the embedded browser is enabled. */
export const Required: Story = {}

/** Auto remains inherited while the server reports the effective capability. */
export const InheritedAuto: Story = {
  args: {
    configuration: fullyInheritedSourceConfiguration,
  },
}

/** An in-flight policy update locks only its own row. */
export const Saving: Story = {
  args: {
    action: {
      sourceId: fullyInheritedSourceConfiguration.source.sourceId,
      key: 'kcefPolicy',
      saving: true,
      error: null,
    },
  },
}

/** A rejected policy keeps the confirmed effective state visible for correction. */
export const Error: Story = {
  args: {
    action: {
      sourceId: fullyInheritedSourceConfiguration.source.sourceId,
      key: 'kcefPolicy',
      saving: false,
      error: 'Required is incompatible with SOCKS routing.',
    },
  },
}
