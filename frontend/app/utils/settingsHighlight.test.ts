/**
 * Settings route state. These tests pin the public deep-link grammar rather
 * than Vue Router internals: only known panes and source-setting rows survive,
 * while source identities remain opaque strings all the way through a
 * parse/build round trip.
 */
import { describe, expect, it } from 'vitest'
import {
  buildSettingsQuery,
  parseSettingsHighlight,
  SETTINGS_PANES,
  SOURCE_CONFIGURATION_ROW_KEYS,
} from './settingsHighlight'

describe('parseSettingsHighlight', () => {
  it('accepts every canonical pane and defaults unknown or malformed panes to Library', () => {
    for (const pane of SETTINGS_PANES) {
      expect(parseSettingsHighlight({ pane }).pane).toBe(pane)
    }

    expect(parseSettingsHighlight({ pane: 'serverConfig' }).pane).toBe('library')
    expect(parseSettingsHighlight({ pane: ['download-engine'] }).pane).toBe('library')
    expect(parseSettingsHighlight({ pane: null }).pane).toBe('library')
  })

  it('keeps arbitrary non-blank source IDs losslessly only in Download engine context', () => {
    const source = '+009127482910938471028:mirror/A'

    expect(parseSettingsHighlight({ pane: 'download-engine', source })).toEqual({
      pane: 'download-engine',
      source,
      setting: null,
    })
    expect(parseSettingsHighlight({ pane: 'library', source }).source).toBeNull()
    expect(parseSettingsHighlight({ pane: 'download-engine', source: '' }).source).toBeNull()
    expect(parseSettingsHighlight({ pane: 'download-engine', source: '   ' }).source).toBeNull()
    expect(parseSettingsHighlight({ pane: 'download-engine', source: [source] }).source).toBeNull()
  })

  it('accepts only known row keys and requires a valid source for row context', () => {
    for (const setting of SOURCE_CONFIGURATION_ROW_KEYS) {
      expect(parseSettingsHighlight({
        pane: 'download-engine',
        source: 'source-A',
        setting,
      }).setting).toBe(setting)
    }

    expect(parseSettingsHighlight({
      pane: 'download-engine',
      source: 'source-A',
      setting: 'unknown-row',
    }).setting).toBeNull()
    expect(parseSettingsHighlight({
      pane: 'download-engine',
      setting: 'downloadConcurrency',
    }).setting).toBeNull()
    expect(parseSettingsHighlight({
      pane: 'download-engine',
      source: 'source-A',
      setting: ['downloadConcurrency'],
    }).setting).toBeNull()
  })
})

describe('buildSettingsQuery', () => {
  it('round-trips the complete canonical contextual route without coercing the source', () => {
    const state = {
      pane: 'download-engine' as const,
      source: '9127482910938471028/raw:source',
      setting: 'imageRequestDelay' as const,
    }

    expect(buildSettingsQuery(state)).toEqual({
      pane: 'download-engine',
      source: '9127482910938471028/raw:source',
      setting: 'imageRequestDelay',
    })
    expect(parseSettingsHighlight(buildSettingsQuery(state))).toEqual(state)
  })

  it('drops source context outside Download engine and never emits a setting without a source', () => {
    expect(buildSettingsQuery({ pane: 'trackers', source: '42', setting: 'imageProxy' })).toEqual({
      pane: 'trackers',
    })
    expect(buildSettingsQuery({ pane: 'download-engine', source: null, setting: 'imageProxy' })).toEqual({
      pane: 'download-engine',
    })
  })
})
