import type { Meta, StoryObj } from '@storybook/vue3'
import { computed, ref } from 'vue'
import Settings from './Settings.vue'
import type { SettingsPane } from './settings.types'
import {
  availableExtensions,
  comicAsuraSourceConfiguration,
  engineInfo,
  extCheckInterval,
  flareSolverrConfig,
  fullyInheritedSourceConfiguration,
  hiveProxySourceConfiguration,
  impersonateConfig,
  installedExtensions,
  librarySettings,
  networkEndpoints,
  pendingSourceException,
  repos,
  settingsCategories,
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
  impersonate: impersonateConfig,
  extensions: installedExtensions,
  availableExtensions,
  repos,
  extCheckInterval,
  sourcesSettings,
  networkEndpoints,
  sourceCatalog: [
    fullyInheritedSourceConfiguration.source,
    comicAsuraSourceConfiguration.source,
    hiveProxySourceConfiguration.source,
  ],
  sourceSummaries: [pendingSourceException],
  selectedSourceId: comicAsuraSourceConfiguration.source.sourceId,
  sourceConfiguration: comicAsuraSourceConfiguration,
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

/** Library pane — metadata/system plus owner-triggered maintenance actions. */
export const Library: Story = {
  render: () => withPane('library'),
}

/** Categories pane — the user-definable category CRUD list (Other is the default). */
export const Categories: Story = {
  render: () => withPane('categories'),
}

/** Engine diagnostics — embedded engine status with a mid-flight upgrade stepper. */
export const Engine: Story = {
  render: () => withPane('engine', { upgradeSteps: upgradeStepsInProgress, upgrading: true }),
}

/** Consolidated Download engine with its complete source editor. */
export const DownloadEngine: Story = {
  render: () => withPane('download-engine'),
}

/** Contextual shortcut — selected source with its canonical pacing row focused. */
export const DownloadEngineContext: Story = {
  render: () => withPane('download-engine', {
    highlightedSourceId: comicAsuraSourceConfiguration.source.sourceId,
    highlightedSetting: 'imageRequestDelay',
  }),
}

/** Sources & Extensions — installed / available / repositories segments. */
export const Extensions: Story = {
  render: () => withPane('extensions'),
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
