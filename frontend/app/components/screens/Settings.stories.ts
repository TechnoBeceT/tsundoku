import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import Settings from './Settings.vue'
import type { SettingsPane } from './settings.types'
import {
  availableExtensions,
  engineInfo,
  extCheckInterval,
  installedExtensions,
  librarySettings,
  repos,
  settingsCategories,
  suwayomiConfig,
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
const meta = {
  title: 'Screens/Settings',
  component: Settings,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof Settings>

export default meta
type Story = StoryObj<typeof meta>

/** Shared props every story passes through to the screen. */
const baseProps = {
  library: librarySettings,
  system: systemInfo,
  categories: settingsCategories,
  engine: engineInfo,
  suwayomi: suwayomiConfig,
  extensions: installedExtensions,
  availableExtensions,
  repos,
  extCheckInterval,
}

/**
 * Renders the screen with a live `activePane` so the sidebar actually switches.
 * `extra` overlays any per-story prop tweaks (e.g. an in-flight upgrade).
 */
const withPane = (startPane: SettingsPane, extra: Record<string, unknown> = {}) => ({
  components: { Settings },
  setup() {
    const activePane = ref<SettingsPane>(startPane)
    return { activePane, baseProps, extra }
  },
  template: `
    <Settings
      v-bind="baseProps"
      :active-pane="activePane"
      v-bind="extra"
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

/** Suwayomi server config — read-only DB + SOCKS (off) + FlareSolverr (on). */
export const SuwayomiConfig: Story = {
  render: () => withPane('suwayomi'),
}

/** Sources & Extensions — installed / available / repositories segments. */
export const Extensions: Story = {
  render: () => withPane('extensions'),
}
