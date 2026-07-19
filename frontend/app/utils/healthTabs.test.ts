/**
 * healthTabs — deep-link + sessionStorage tab resolution.
 *
 * Pins the resolution precedence the Health console relies on:
 *   1. a valid `?tab=` query wins over everything (deep-link always lands),
 *      including the `metrics` → sources ALIAS (slice-5 alert badge),
 *   2. else the persisted session tab,
 *   3. else the `library` default,
 *   with unknown values in either input ignored.
 *
 * Non-vacuous: drop the `metrics` alias and the alias assertion fails on
 * 'library'; let an unknown query win and the fallback assertions fail; drop the
 * query-over-stored precedence and that assertion returns 'library'.
 */
import { describe, it, expect } from 'vitest'
import { resolveInitialHealthTab, HEALTH_TABS, HEALTH_TAB_SESSION_KEY } from './healthTabs'

describe('resolveInitialHealthTab', () => {
  it('defaults to library when nothing is set', () => {
    expect(resolveInitialHealthTab(null, null)).toBe('library')
  })

  it('honours a ?tab=sources deep-link', () => {
    expect(resolveInitialHealthTab('sources', null)).toBe('sources')
  })

  it('treats ?tab=metrics as an alias for the sources tab', () => {
    expect(resolveInitialHealthTab('metrics', null)).toBe('sources')
  })

  it('honours a ?tab=library deep-link', () => {
    expect(resolveInitialHealthTab('library', null)).toBe('library')
  })

  it('falls back to the stored tab when the query is absent', () => {
    expect(resolveInitialHealthTab(null, 'sources')).toBe('sources')
  })

  it('ignores an unknown query and falls back to the stored tab', () => {
    expect(resolveInitialHealthTab('bogus', 'sources')).toBe('sources')
  })

  it('ignores an unknown stored value and uses the default', () => {
    expect(resolveInitialHealthTab(null, 'garbage')).toBe('library')
  })

  it('lets a valid query win over a conflicting stored tab', () => {
    expect(resolveInitialHealthTab('sources', 'library')).toBe('sources')
  })

  it('lets the attention-pill ?tab=library win over a stored sources tab', () => {
    // The AppShell "N need attention" pill navigates to /health?tab=library (a
    // SERIES-health signal → the sick-series Library view). The forced query
    // MUST beat a sessionStorage tab of 'sources', else the pill would wrongly
    // drop the owner on the Sources metrics tab — the exact slice-3 failure.
    expect(resolveInitialHealthTab('library', 'sources')).toBe('library')
  })
})

describe('healthTabs constants', () => {
  it('exposes the two tabs in order (library first)', () => {
    expect(HEALTH_TABS.map(t => t.key)).toEqual(['library', 'sources'])
  })

  it('uses a stable sessionStorage key', () => {
    expect(HEALTH_TAB_SESSION_KEY).toBe('tsundoku.health.tab')
  })
})
