/**
 * useReaderSettings — the reader's GLOBAL display preferences, persisted to
 * localStorage.
 *
 * These are app-wide (not per-series) knobs the reader-settings sheet edits and
 * the long-strip reader applies via CSS custom properties:
 *   - `sidePaddingPct` — horizontal breathing room around the page column (%).
 *   - `fit`            — 'max' caps the column at `maxWidthPx`; 'width' lets it
 *                        fill the viewport.
 *   - `maxWidthPx`     — the column cap (only meaningful when `fit === 'max'`).
 *   - `gaps`           — whether a vertical gap sits between stacked pages.
 *
 * localStorage is the source of truth: `useReaderSettings()` HYDRATES a reactive
 * view from storage on call and every `update()` WRITES THROUGH immediately, so a
 * change made in one reader mount is visible to the next (and survives a reload).
 * The reader route holds the single live instance and passes `settings` down to
 * the presentation-only sheet — so there is no cross-instance live-sync to worry
 * about (the sheet never calls this itself).
 *
 * Everything is guarded + fail-soft: a missing `window` (SSR/tests), an absent
 * key, corrupt JSON, or a wrong-typed field all fall back to the default for that
 * field — reading settings can never throw.
 */
import { reactive } from 'vue'

/** The reader's global display preferences (see the file header). */
export interface ReaderSettings {
  /** Horizontal padding around the page column, as a percentage (0–25). */
  sidePaddingPct: number
  /** Column sizing: 'max' caps at `maxWidthPx`, 'width' fills the viewport. */
  fit: 'width' | 'max'
  /** Column cap in px when `fit === 'max'`. */
  maxWidthPx: number
  /** Whether a vertical gap separates stacked pages. */
  gaps: boolean
}

/** The out-of-the-box defaults used when nothing (valid) is stored. */
export const READER_SETTINGS_DEFAULTS: ReaderSettings = {
  sidePaddingPct: 0,
  fit: 'max',
  maxWidthPx: 800,
  gaps: false,
}

/** localStorage key the preferences are persisted under. */
const STORAGE_KEY = 'tsundoku.reader.settings'

/** Returns `v` when it is a finite number, else `fallback` (guards NaN/strings/null). */
function numberOr(v: unknown, fallback: number): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback
}

/**
 * sanitize — coerces an untrusted partial (parsed JSON, a caller patch) into a
 * complete, valid ReaderSettings, substituting the default for any missing or
 * wrong-typed field. Never throws.
 */
function sanitize(raw: Partial<ReaderSettings> | null | undefined): ReaderSettings {
  const d = READER_SETTINGS_DEFAULTS
  return {
    sidePaddingPct: numberOr(raw?.sidePaddingPct, d.sidePaddingPct),
    fit: raw?.fit === 'width' || raw?.fit === 'max' ? raw.fit : d.fit,
    maxWidthPx: numberOr(raw?.maxWidthPx, d.maxWidthPx),
    gaps: typeof raw?.gaps === 'boolean' ? raw.gaps : d.gaps,
  }
}

/** Reads + sanitizes the stored settings; returns fresh defaults on any problem. */
function load(): ReaderSettings {
  if (typeof window === 'undefined') return { ...READER_SETTINGS_DEFAULTS }
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY)
    if (!stored) return { ...READER_SETTINGS_DEFAULTS }
    return sanitize(JSON.parse(stored) as Partial<ReaderSettings>)
  }
  catch {
    // Corrupt JSON / unavailable storage — fall back, never surface an error.
    return { ...READER_SETTINGS_DEFAULTS }
  }
}

/** Best-effort persist: swallows a storage failure (private mode / quota). */
function persist(settings: ReaderSettings): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(settings))
  }
  catch {
    // Non-fatal — the in-memory settings still apply for this session.
  }
}

/**
 * readerStyleVars — maps the settings to the CSS custom properties the reader
 * strip reads. A PURE mapping (settings in, style object out) so it is trivially
 * unit-tested and reused wherever the strip container is styled:
 *   - `--reader-col-max`  — the column max-width ('none' when fitting to width).
 *   - `--reader-side-pad` — horizontal padding as a percentage.
 *   - `--reader-page-gap` — the inter-page gap (0 when gaps are off).
 */
export function readerStyleVars(settings: ReaderSettings): Record<string, string> {
  return {
    '--reader-col-max': settings.fit === 'max' ? `${settings.maxWidthPx}px` : 'none',
    '--reader-side-pad': `${settings.sidePaddingPct}%`,
    '--reader-page-gap': settings.gaps ? '12px' : '0px',
  }
}

/**
 * useReaderSettings — see the file header. Returns the reactive `settings` (a
 * hydrated view of localStorage) plus `update(patch)`, which merges + sanitizes
 * the patch, applies it reactively, and writes it through to storage.
 */
export function useReaderSettings() {
  const settings = reactive<ReaderSettings>(load())

  /** Merge a partial change into the settings and persist it immediately. */
  function update(patch: Partial<ReaderSettings>): void {
    Object.assign(settings, sanitize({ ...settings, ...patch }))
    persist(settings)
  }

  return { settings, update }
}
