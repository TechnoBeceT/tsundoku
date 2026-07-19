import type { Meta, StoryObj } from '@storybook/vue3'
import { computed, ref } from 'vue'
import Settings from './Settings.vue'
import type { SettingsPane } from './settings.types'
import {
  availableExtensions,
  engineInfo,
  extCheckInterval,
  flareSolverrConfig,
  installedExtensions,
  librarySettings,
  networkEndpoints,
  networkSources,
  repos,
  settingsCategories,
  sourceBindings,
  sourcesSettings,
  systemInfo,
  upgradeStepsInProgress,
} from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Settings screen — one per pane. Flip the Storybook theme
 * toolbar to confirm each pane reads correctly in BOTH dark and light. Each
 * story opens on its pane; the sidebar nav stays interactive (clicking switches
 * panes) via a live `activePane` ref.
 */
/** Shared props every story passes through to the screen. */
const baseProps = {
  library: librarySettings,
  system: systemInfo,
  categories: settingsCategories,
  engine: engineInfo,
  flareSolverr: flareSolverrConfig,
  extensions: installedExtensions,
  availableExtensions,
  repos,
  extCheckInterval,
  sourcesSettings,
  networkEndpoints,
  networkSources,
  networkBindings: sourceBindings,
}

const meta = {
  title: 'Screens/Settings',
  component: Settings,
  parameters: { layout: 'fullscreen' },
  // The screen's required props (library/system/engine/flareSolverr/…); the interactive
  // stories pass these via the withPane wrapper, so this default satisfies the
  // CSF3 story typing (baseProps covers exactly the required set).
  args: baseProps,
} satisfies Meta<typeof Settings>

export default meta
type Story = StoryObj<typeof meta>

/**
 * Renders the screen with a live `activePane` so the sidebar actually switches.
 * `extra` overlays any per-story prop tweaks (e.g. an in-flight upgrade).
 */
const withPane = (startPane: SettingsPane, extra: Record<string, unknown> = {}) => ({
  components: { Settings },
  setup() {
    const activePane = ref<SettingsPane>(startPane)
    // `extra` overrides `baseProps` on key collisions — mirrors the previous
    // dual v-bind's "later wins" order (Vue rejects two bare v-bind on one element).
    const mergedProps = computed(() => ({ ...baseProps, ...extra }))
    return { activePane, mergedProps }
  },
  template: `
    <Settings
      v-bind="mergedProps"
      :active-pane="activePane"
      @set-pane="activePane = $event"
    />
  `,
})

/** Library pane — Schedules & Behavior knobs + the read-only System card. */
export const Library: Story = {
  render: () => withPane('library'),
}

/** Categories pane — the user-definable category CRUD list (Other protected). */
export const Categories: Story = {
  render: () => withPane('categories'),
}

/** Engine pane — embedded engine status with a mid-flight upgrade stepper. */
export const Engine: Story = {
  render: () => withPane('engine', { upgradeSteps: upgradeStepsInProgress, upgrading: true }),
}

/** Server config pane — the Tsundoku-owned FlareSolverr card (on). */
export const ServerConfig: Story = {
  render: () => withPane('serverConfig'),
}

/** Sources & Extensions — installed / available / repositories segments. */
export const Extensions: Story = {
  render: () => withPane('extensions'),
}

/** Sources pane — the warm-up/circuit-breaker CONFIG knobs + library-maintenance
 *  dedup sweep (the search-metrics report moved to the /health Source Health tab). */
export const Sources: Story = {
  render: () => withPane('sources'),
}

/** Network pane — per-source SOCKS/FlareSolverr routing (endpoints + assignment). */
export const Network: Story = {
  render: () => withPane('network'),
}

/**
 * Extensions §16: one row mid-update (busy spinner + disabled) and a pane-level
 * failure banner — the per-row mutation no longer fires into the void.
 */
export const ExtensionsBusy: Story = {
  render: () => withPane('extensions', {
    extensionAction: { busyId: 'asurascans', error: 'Update failed — 502 from the extension repository.' },
  }),
}

/**
 * Categories §16: one row mid-mutation (busy spinner + disabled controls) plus a
 * failed-move error surfaced inline, not just a silent spinner.
 */
export const CategoriesBusy: Story = {
  render: () => withPane('categories', {
    categoryAction: { busyId: 'cat-manhwa', error: 'Folder move failed — the target name already exists on disk.' },
  }),
}

/**
 * Uninstall confirmation (brief §2e): the play fn clicks the first "Uninstall"
 * to open the destructive (red) confirm modal — uninstall never fires directly.
 */
export const ExtensionsUninstallConfirm: Story = {
  render: () => withPane('extensions'),
  play: ({ canvasElement }: { canvasElement: HTMLElement }) => {
    const btn = [...canvasElement.querySelectorAll('button')]
      .find((b) => b.textContent?.trim() === 'Uninstall')
    btn?.click()
  },
}
