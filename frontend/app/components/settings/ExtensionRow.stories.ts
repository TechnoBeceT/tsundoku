import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, fn, userEvent, waitFor, within } from 'storybook/test'
import ExtensionRow from './ExtensionRow.vue'
import { availableExtensions, installedExtensions } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

// A tiny inline data-URI icon fixture (a 4x4 red PNG) — Storybook has no
// backend to proxy a real Suwayomi icon from, so this stands in for a
// successfully-loaded iconUrl.
const fakeIconDataUri
  = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAQAAAAECAYAAACp8Z5+AAAAEUlEQVR42mP8z8BQz0AEYBxVAgB6nwYRWl6tSAAAAABJRU5ErkJggg=='

/**
 * Stories for a single extension card. Flip the Storybook theme toolbar to
 * confirm both dark and light.
 */
const meta = {
  title: 'Settings/ExtensionRow',
  component: ExtensionRow,
  parameters: { layout: 'padded' },
  args: { busy: false },
} satisfies Meta<typeof ExtensionRow>

export default meta
type Story = StoryObj<typeof meta>

/** Installed, up to date — Uninstall only. */
export const Installed: Story = {
  args: { extension: installedExtensions[0]!, installed: true },
}

/** Installed with an update — UPDATE badge + Update + Uninstall. */
export const InstalledWithUpdate: Story = {
  args: { extension: installedExtensions[1]!, installed: true },
}

/** Available — the Install action. */
export const Available: Story = {
  args: { extension: availableExtensions[0]!, installed: false },
}

/** §16 busy — the acting button spins and the row dims/disables. */
export const Busy: Story = {
  args: { extension: installedExtensions[1]!, installed: true, busy: true },
}

/**
 * Reversible updates: an installed extension with a rollback HISTORY. The
 * "History (3)" toggle reveals the held versions — the current one tagged
 * "Current", the older two each offering "Reinstall this version". The play
 * function expands the history and clicks the newest older build's reinstall,
 * asserting it emits that version code.
 */
export const WithVersionHistory: Story = {
  args: { extension: installedExtensions[1]!, installed: true, onReinstall: fn() },
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByRole('button', { name: /History \(3\)/ }))
    // Three held rows; the current version (49) shows "Current", the other two
    // offer a reinstall — so exactly two "Reinstall this version" buttons.
    await waitFor(async () => {
      await expect(canvasElement.querySelectorAll('.ext-history__row').length).toBe(3)
    })
    const reinstalls = canvas.getAllByRole('button', { name: /Reinstall this version/ })
    await expect(reinstalls.length).toBe(2)
    await userEvent.click(reinstalls[0]!)
    // The first older build in the newest-first list is versionCode 48.
    await expect(args.onReinstall).toHaveBeenCalledWith(48)
  },
}

/**
 * A real (proxied) icon renders in place of the tinted placeholder square —
 * confirms the M1 icon-proxy fix's <img> path, not just the fallback.
 */
export const WithIcon: Story = {
  args: {
    extension: { ...installedExtensions[0]!, iconUrl: fakeIconDataUri },
    installed: true,
  },
}

/**
 * A broken/unreachable iconUrl (a 502 from the proxy, or Storybook's lack of a
 * backend) falls back to the tinted placeholder square via the <img>'s
 * `@error` handler — the row never shows a broken-image glyph. The play
 * function waits for the real <img> load failure to propagate.
 */
export const IconLoadError: Story = {
  args: {
    extension: { ...installedExtensions[0]!, iconUrl: '/api/suwayomi/extensions/does-not-exist/icon' },
    installed: true,
  },
  play: async ({ canvasElement }) => {
    // Query the DOM node directly (not by role): the <img> is aria-hidden by
    // design, so a role-based query would pass vacuously whether or not the
    // fallback actually kicked in.
    await waitFor(async () => {
      await expect(canvasElement.querySelector('img.ext-card__icon')).toBeNull()
    })
    await expect(canvasElement.querySelector('span.ext-card__avatar')).not.toBeNull()
  },
}
