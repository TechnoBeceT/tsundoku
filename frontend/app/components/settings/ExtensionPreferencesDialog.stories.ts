import type { Meta, StoryObj } from '@storybook/vue3'
import ExtensionPreferencesDialog from './ExtensionPreferencesDialog.vue'
import { preferenceGroups } from '../../fixtures/preferences'
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the extension "Configure" dialog. The dialog is presentation-only
 * (open + groups + §16 state in, change out), so every state is a pure
 * fixture: loaded (all variants across two language sources, the 2nd DISABLED
 * so its preference block is collapsed), loading, error, empty, a per-row
 * saving spinner, and the per-language enable/disable Switch busy + error.
 * Flip the theme toolbar for dark/light.
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
    enablingKey: null,
    enableError: null,
    ignoringKey: null,
    ignoreError: null,
  },
} satisfies Meta<typeof ExtensionPreferencesDialog>

export default meta
type Story = StoryObj<typeof meta>

/**
 * Loaded — every control variant across two language sources; the second
 * language (JA) is DISABLED, so its preference block is collapsed to the
 * "Disabled — hidden from Discover…" note behind an off Switch (feature #2).
 */
export const Loaded: Story = {}

/** Both languages enabled — neither preference block is collapsed. */
export const AllEnabled: Story = {
  args: {
    groups: preferenceGroups.map(g => ({ ...g, enabled: true })),
  },
}

/** The JA source's enable/disable Switch write is in flight (spinner + disabled). */
export const EnableToggling: Story = {
  args: { enablingKey: 'src-ja' },
}

/** An enable/disable write failed; the error banners at the top of the dialog. */
export const EnableError: Story = {
  args: { enableError: 'Failed to update source' },
}

/**
 * The EN source flagged ignore-scanlator (its per-source Toggle is on) — future
 * adopts collapse its per-uploader providers into one [Source] provider.
 */
export const IgnoreScanlatorOn: Story = {
  args: {
    groups: preferenceGroups.map(g => (g.sourceId === 'src-en' ? { ...g, ignoreScanlator: true } : g)),
  },
}

/** The EN source's ignore-scanlator Toggle write is in flight (spinner + disabled). */
export const IgnoreScanlatorToggling: Story = {
  args: { ignoringKey: 'src-en' },
}

/** An ignore-scanlator write failed; the error banners at the top of the dialog. */
export const IgnoreScanlatorError: Story = {
  args: { ignoreError: 'Failed to update source' },
}

/**
 * The on-enable collapse migration ran: flipping the flag ON folded already-
 * adopted per-uploader providers into one [Source] provider and relabeled their
 * files — surfaced as a success banner so the destructive migration is not silent.
 */
export const IgnoreScanlatorMigrated: Story = {
  args: {
    groups: preferenceGroups.map(g => (g.sourceId === 'src-en' ? { ...g, ignoreScanlator: true } : g)),
    migrationMessage: {
      message: 'Merged 4 per-uploader providers across 3 series and relabeled their files.',
      tone: 'success',
    },
  },
}

/**
 * The on-enable migration FAILED for every affected series — nothing was
 * relabeled. A warning banner makes the total failure loud (not a silent success)
 * so the owner knows to check the logs and retry.
 */
export const IgnoreScanlatorMigrationFailed: Story = {
  args: {
    groups: preferenceGroups.map(g => (g.sourceId === 'src-en' ? { ...g, ignoreScanlator: true } : g)),
    migrationMessage: {
      message: 'Couldn\'t collapse 3 series — nothing was relabeled. Check the logs and try again.',
      tone: 'warning',
    },
  },
}

/** The initial load is in flight. */
export const Loading: Story = {
  args: { groups: [], pending: true },
}

/** A load failure. */
export const LoadError: Story = {
  args: { groups: [], error: 'The engine host was unreachable' },
}

/** An extension with no configurable preferences. */
export const Empty: Story = {
  args: { groups: [] },
}

/** §16 — a per-row write is in flight (the Data saver switch on src-en). */
export const Saving: Story = {
  args: { savingKey: 'src-en:dataSaver_en' },
}

/** §16 — a write failed; the error banners at the top of the dialog. */
export const SaveError: Story = {
  args: { saveError: 'The engine host rejected the change' },
}
