import type {
  SettingsPane,
  SourceConfigurationRowKey,
} from '~/components/screens/settings.types'

/** Canonical Settings destinations accepted from `?pane=`. */
export const SETTINGS_PANES = [
  'library',
  'categories',
  'download-engine',
  'engine',
  'extensions',
  'trackers',
  'notifications',
] as const satisfies readonly SettingsPane[]

/** Canonical source-setting targets accepted from `?setting=`. */
export const SOURCE_CONFIGURATION_ROW_KEYS = [
  'downloadConcurrency',
  'imageRequestDelay',
  'byparr',
  'reuseBypassSession',
  'imageConnectionMode',
  'imageProxy',
  'socksBinding',
  'bypassBinding',
] as const satisfies readonly SourceConfigurationRowKey[]

export interface SettingsHighlight {
  pane: SettingsPane
  source: string | null
  setting: SourceConfigurationRowKey | null
}

type QueryValue = string | null | undefined | readonly (string | null)[]
type SettingsQuery = Record<string, QueryValue>

const paneSet = new Set<string>(SETTINGS_PANES)
const settingSet = new Set<string>(SOURCE_CONFIGURATION_ROW_KEYS)

function singleString(value: QueryValue): string | null {
  return typeof value === 'string' ? value : null
}

/** Parse Settings query state without coercing source identity strings. */
export function parseSettingsHighlight(query: SettingsQuery): SettingsHighlight {
  const rawPane = singleString(query.pane)
  const pane = rawPane != null && paneSet.has(rawPane)
    ? rawPane as SettingsPane
    : 'library'

  if (pane !== 'download-engine') return { pane, source: null, setting: null }

  const rawSource = singleString(query.source)
  const source = rawSource != null && rawSource.trim().length > 0 ? rawSource : null
  if (source == null) return { pane, source: null, setting: null }

  const rawSetting = singleString(query.setting)
  const setting = rawSetting != null && settingSet.has(rawSetting)
    ? rawSetting as SourceConfigurationRowKey
    : null

  return { pane, source, setting }
}

/** Build the canonical Settings query, keeping context only in Download engine. */
export function buildSettingsQuery(state: SettingsHighlight): Record<string, string> {
  const query: Record<string, string> = { pane: state.pane }
  if (state.pane !== 'download-engine' || state.source == null || state.source.trim().length === 0) {
    return query
  }

  query.source = state.source
  if (state.setting != null) query.setting = state.setting
  return query
}
