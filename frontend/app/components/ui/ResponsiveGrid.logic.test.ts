import { describe, expect, it } from 'vitest'
import { gridStyleVars, pxToRem } from './ResponsiveGrid.logic'

describe('pxToRem', () => {
  it('converts a plain px string to rem against the 16px base', () => {
    expect(pxToRem('186px')).toBe('11.625rem')
    expect(pxToRem('16px')).toBe('1rem')
    expect(pxToRem('132px')).toBe('8.25rem')
    expect(pxToRem('300px')).toBe('18.75rem')
  })

  it('handles fractional + whitespace-padded px', () => {
    expect(pxToRem(' 13.5px ')).toBe('0.84375rem')
  })

  it('passes non-plain-px values straight through (tokens, %, rem, clamp)', () => {
    expect(pxToRem('var(--space-xl)')).toBe('var(--space-xl)')
    expect(pxToRem('100%')).toBe('100%')
    expect(pxToRem('1rem')).toBe('1rem')
    expect(pxToRem('clamp(8rem, 20vw, 12rem)')).toBe('clamp(8rem, 20vw, 12rem)')
  })
})

describe('gridStyleVars', () => {
  it('emits a fluid, overflow-guarded track template + gap', () => {
    const vars = gridStyleVars({ minTile: '186px', gap: 'var(--space-xl)', fill: 'auto-fill' })
    expect(vars['--rg-cols']).toBe('repeat(auto-fill, minmax(min(11.625rem, 100%), 1fr))')
    expect(vars['--rg-gap']).toBe('var(--space-xl)')
  })

  it('honours the auto-fit fill mode (Categories\' intentional difference)', () => {
    const vars = gridStyleVars({ minTile: '240px', gap: 'var(--space-lg)', fill: 'auto-fit' })
    expect(vars['--rg-cols']).toBe('repeat(auto-fit, minmax(min(15rem, 100%), 1fr))')
  })

  it('omits the mobile + phone vars entirely when no override is given', () => {
    const vars = gridStyleVars({ minTile: '300px', gap: 'var(--space-base)', fill: 'auto-fill' })
    expect(vars).not.toHaveProperty('--rg-cols-mobile')
    expect(vars).not.toHaveProperty('--rg-gap-mobile')
    expect(vars).not.toHaveProperty('--rg-cols-phone')
  })

  it('emits the mobile track + gap only when their overrides are given', () => {
    const vars = gridStyleVars({
      minTile: '184px',
      gap: 'var(--space-xl)',
      fill: 'auto-fill',
      mobileMinTile: '132px',
      mobileGap: 'var(--space-sm)',
    })
    expect(vars['--rg-cols-mobile']).toBe('repeat(auto-fill, minmax(min(8.25rem, 100%), 1fr))')
    expect(vars['--rg-gap-mobile']).toBe('var(--space-sm)')
  })
})

/* The PHONE band (QCAT-263): the count is HELD and the tiles grow, so the track
 * template is a fixed `repeat(<n>, …)` — NOT an `auto-*` floor. The `0` floor is
 * the zero-overflow guard's phone-band form: a held count must still be able to
 * shrink into whatever the viewport gives (QCAT-230). */
describe('gridStyleVars — phoneColumns (the held-column phone mode)', () => {
  it('emits a HELD track template with a zero floor, not an auto-* floor', () => {
    const vars = gridStyleVars({
      minTile: '186px',
      gap: 'var(--space-xl)',
      fill: 'auto-fill',
      phoneColumns: 3,
    })
    expect(vars['--rg-cols-phone']).toBe('repeat(3, minmax(0, 1fr))')
  })

  it('leaves the desktop + mobile bands on auto-fill (the hold is phone-only)', () => {
    const vars = gridStyleVars({
      minTile: '186px',
      gap: 'var(--space-xl)',
      fill: 'auto-fill',
      mobileMinTile: '112px',
      phoneColumns: 3,
    })
    expect(vars['--rg-cols']).toBe('repeat(auto-fill, minmax(min(11.625rem, 100%), 1fr))')
    expect(vars['--rg-cols-mobile']).toBe('repeat(auto-fill, minmax(min(7rem, 100%), 1fr))')
  })

  it('holds the count the caller asked for (2 for Categories, 1 for Health)', () => {
    expect(gridStyleVars({ minTile: '240px', gap: '0', fill: 'auto-fit', phoneColumns: 2 })['--rg-cols-phone'])
      .toBe('repeat(2, minmax(0, 1fr))')
    expect(gridStyleVars({ minTile: '300px', gap: '0', fill: 'auto-fill', phoneColumns: 1 })['--rg-cols-phone'])
      .toBe('repeat(1, minmax(0, 1fr))')
  })

  it('floors the count at 1 — `repeat(0, …)` is invalid CSS and would drop the whole template', () => {
    expect(gridStyleVars({ minTile: '186px', gap: '0', fill: 'auto-fill', phoneColumns: 0 })['--rg-cols-phone'])
      .toBe('repeat(1, minmax(0, 1fr))')
    expect(gridStyleVars({ minTile: '186px', gap: '0', fill: 'auto-fill', phoneColumns: -3 })['--rg-cols-phone'])
      .toBe('repeat(1, minmax(0, 1fr))')
  })

  it('truncates a fractional count rather than emitting `repeat(2.5, …)`', () => {
    expect(gridStyleVars({ minTile: '186px', gap: '0', fill: 'auto-fill', phoneColumns: 2.5 })['--rg-cols-phone'])
      .toBe('repeat(2, minmax(0, 1fr))')
  })
})
