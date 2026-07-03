/**
 * Fixtures for the extension per-source "Configure" dialog — a source group with
 * one preference of each union variant, so stories + Vitest exercise every
 * control (Toggle / Select / multi-select / TextField) without a backend.
 */
import type { components } from '~/utils/api/schema.d.ts'

type Preference = components['schemas']['SourcePreference']
type Group = components['schemas']['SourcePreferencesGroup']

/** A Switch preference (currently on). */
export const switchPref: Preference = {
  type: 'SwitchPreference',
  position: 0,
  key: 'dataSaver_en',
  title: 'Data saver',
  summary: 'Load smaller, lower-quality images',
  currentValue: true,
  default: false,
  entries: [],
  entryValues: [],
}

/** A List preference (single choice). */
export const listPref: Preference = {
  type: 'ListPreference',
  position: 1,
  key: 'thumbnailQuality_en',
  title: 'Thumbnail quality',
  summary: '',
  currentValue: '.512.jpg',
  default: '',
  entries: ['Original', 'Medium', 'Low'],
  entryValues: ['', '.512.jpg', '.256.jpg'],
}

/** A MultiSelectList preference (subset selected). */
export const multiPref: Preference = {
  type: 'MultiSelectListPreference',
  position: 2,
  key: 'contentRating_en',
  title: 'Content rating',
  summary: 'Which ratings to include',
  currentValue: ['safe', 'suggestive'],
  default: ['safe'],
  entries: ['Safe', 'Suggestive', 'Erotica'],
  entryValues: ['safe', 'suggestive', 'erotica'],
}

/** An EditText preference (currently empty). */
export const editPref: Preference = {
  type: 'EditTextPreference',
  position: 3,
  key: 'blockedGroups_en',
  title: 'Blocked groups',
  summary: 'Comma-separated scanlation groups to hide',
  currentValue: null,
  default: null,
  entries: [],
  entryValues: [],
}

/** One source's group covering all four control variants (enabled). */
export const preferenceGroup: Group = {
  sourceId: 'src-en',
  sourceName: 'MangaDex',
  lang: 'en',
  enabled: true,
  preferences: [switchPref, listPref, multiPref, editPref],
}

/**
 * A two-language grouping (an extension backs one source per language) — the
 * second language is DISABLED, so stories + Vitest exercise the per-language
 * enable/disable Switch without a backend.
 */
export const preferenceGroups: Group[] = [
  preferenceGroup,
  {
    sourceId: 'src-ja',
    sourceName: 'MangaDex',
    lang: 'ja',
    enabled: false,
    preferences: [{ ...switchPref, position: 0, currentValue: false }],
  },
]
