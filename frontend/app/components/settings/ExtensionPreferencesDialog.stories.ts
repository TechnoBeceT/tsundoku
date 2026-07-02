import type { Meta, StoryObj } from '@storybook/vue3'
import ExtensionPreferencesDialog from './ExtensionPreferencesDialog.vue'
import { preferenceGroups } from '../../fixtures/preferences'
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the extension "Configure" dialog. The dialog is presentation-only
 * (open + groups + §16 state in, change out), so every state is a pure fixture:
 * loaded (all variants across two language sources), loading, error, empty, and
 * a per-row saving spinner. Flip the theme toolbar for dark/light.
 */
const meta = {
  title: 'Settings/ExtensionPreferencesDialog',
  component: ExtensionPreferencesDialog,
  parameters: { layout: 'fullscreen' },
  args: {
    open: true,
    extensionName: 'MangaDex',
    groups: preferenceGroups,
    pending: false,
    error: null,
    savingKey: null,
    saveError: null,
  },
} satisfies Meta<typeof ExtensionPreferencesDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Loaded — every control variant across two language sources. */
export const Loaded: Story = {}

/** The initial load is in flight. */
export const Loading: Story = {
  args: { groups: [], pending: true },
}

/** A load failure. */
export const LoadError: Story = {
  args: { groups: [], error: 'Suwayomi was unreachable' },
}

/** An extension with no configurable preferences. */
export const Empty: Story = {
  args: { groups: [] },
}

/** §16 — a per-row write is in flight (the Data saver switch on src-en). */
export const Saving: Story = {
  args: { savingKey: 'src-en:0' },
}

/** §16 — a write failed; the error banners at the top of the dialog. */
export const SaveError: Story = {
  args: { saveError: 'Suwayomi rejected the change' },
}
