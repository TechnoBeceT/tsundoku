/**
 * useReaderSettings — the reader's global, localStorage-backed display prefs.
 *
 * Pins:
 *   1. Defaults when storage is empty.
 *   2. Seeds from stored JSON.
 *   3. update() persists through to localStorage AND mutates the reactive view.
 *   4. Corrupt JSON falls back to defaults (never throws).
 *   5. A missing / wrong-typed field falls back to that field's default.
 *   6. readerStyleVars maps settings → the strip's CSS custom properties.
 */
import { describe, it, expect, beforeEach } from 'vitest'
import {
  useReaderSettings,
  readerStyleVars,
  READER_SETTINGS_DEFAULTS,
  type ReaderSettings,
} from './useReaderSettings'

const STORAGE_KEY = 'tsundoku.reader.settings'

beforeEach(() => {
  localStorage.clear()
})

describe('useReaderSettings — hydration', () => {
  it('uses the defaults when nothing is stored', () => {
    const { settings } = useReaderSettings()
    expect({ ...settings }).toEqual(READER_SETTINGS_DEFAULTS)
  })

  it('seeds from stored JSON', () => {
    const stored: ReaderSettings = { sidePaddingPct: 10, fit: 'width', maxWidthPx: 1000, gaps: true }
    localStorage.setItem(STORAGE_KEY, JSON.stringify(stored))
    const { settings } = useReaderSettings()
    expect({ ...settings }).toEqual(stored)
  })

  it('falls back to defaults on corrupt JSON without throwing', () => {
    localStorage.setItem(STORAGE_KEY, '{ not valid json')
    expect(() => useReaderSettings()).not.toThrow()
    const { settings } = useReaderSettings()
    expect({ ...settings }).toEqual(READER_SETTINGS_DEFAULTS)
  })

  it('fills each missing field from the default', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ gaps: true }))
    const { settings } = useReaderSettings()
    expect(settings.gaps).toBe(true)
    expect(settings.sidePaddingPct).toBe(READER_SETTINGS_DEFAULTS.sidePaddingPct)
    expect(settings.fit).toBe(READER_SETTINGS_DEFAULTS.fit)
    expect(settings.maxWidthPx).toBe(READER_SETTINGS_DEFAULTS.maxWidthPx)
  })

  it('rejects a wrong-typed field and uses the default', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ sidePaddingPct: 'lots', fit: 'diagonal', maxWidthPx: null }))
    const { settings } = useReaderSettings()
    expect(settings.sidePaddingPct).toBe(READER_SETTINGS_DEFAULTS.sidePaddingPct)
    expect(settings.fit).toBe(READER_SETTINGS_DEFAULTS.fit)
    expect(settings.maxWidthPx).toBe(READER_SETTINGS_DEFAULTS.maxWidthPx)
  })
})

describe('useReaderSettings — update (write-through)', () => {
  it('mutates the reactive settings and persists the change', () => {
    const { settings, update } = useReaderSettings()
    update({ fit: 'width', sidePaddingPct: 8 })
    expect(settings.fit).toBe('width')
    expect(settings.sidePaddingPct).toBe(8)
    // Persisted so the next mount hydrates the new values.
    const persisted = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}') as ReaderSettings
    expect(persisted).toMatchObject({ fit: 'width', sidePaddingPct: 8 })
  })

  it('a second mount reads back a persisted update (global persistence)', () => {
    useReaderSettings().update({ gaps: true, maxWidthPx: 1200 })
    const { settings } = useReaderSettings()
    expect(settings.gaps).toBe(true)
    expect(settings.maxWidthPx).toBe(1200)
  })

  it('sanitizes an invalid patch value back to the default', () => {
    const { settings, update } = useReaderSettings()
    update({ maxWidthPx: Number.NaN })
    expect(settings.maxWidthPx).toBe(READER_SETTINGS_DEFAULTS.maxWidthPx)
  })
})

describe('readerStyleVars', () => {
  it('caps the column and emits padding + gap for fit=max, gaps on', () => {
    const vars = readerStyleVars({ sidePaddingPct: 6, fit: 'max', maxWidthPx: 900, gaps: true })
    expect(vars).toEqual({
      '--reader-col-max': '900px',
      '--reader-side-pad': '6%',
      '--reader-page-gap': '12px',
    })
  })

  it('drops the cap for fit=width and zeroes the gap when gaps are off', () => {
    const vars = readerStyleVars({ sidePaddingPct: 0, fit: 'width', maxWidthPx: 900, gaps: false })
    expect(vars['--reader-col-max']).toBe('none')
    expect(vars['--reader-page-gap']).toBe('0px')
  })
})
