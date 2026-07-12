import { describe, it, expect } from 'vitest'
import { showTicks, pageToFraction, fractionToPage, SLIDER_TICK_MAX_PAGES } from './ReaderPageSlider.logic'

describe('showTicks', () => {
  it('shows ticks at or below the threshold', () => {
    expect(showTicks(1)).toBe(true)
    expect(showTicks(SLIDER_TICK_MAX_PAGES)).toBe(true)
  })

  it('hides ticks above the threshold — 165 dots is a smear, not a scale', () => {
    expect(showTicks(SLIDER_TICK_MAX_PAGES + 1)).toBe(false)
    expect(showTicks(165)).toBe(false)
  })

  it('hides ticks for an empty chapter', () => {
    expect(showTicks(0)).toBe(false)
  })
})

describe('pageToFraction', () => {
  it('maps first page to 0 and last page to 1', () => {
    expect(pageToFraction(0, 10)).toBe(0)
    expect(pageToFraction(9, 10)).toBe(1)
  })

  it('maps the midpoint', () => {
    expect(pageToFraction(5, 11)).toBeCloseTo(0.5)
  })

  it('is 0 for a single-page chapter (no division by zero)', () => {
    expect(pageToFraction(0, 1)).toBe(0)
  })

  it('is 0 for an empty chapter', () => {
    expect(pageToFraction(0, 0)).toBe(0)
  })
})

describe('fractionToPage', () => {
  it('round-trips with pageToFraction', () => {
    for (const page of [0, 3, 7, 9]) {
      expect(fractionToPage(pageToFraction(page, 10), 10)).toBe(page)
    }
  })

  it('clamps out-of-range fractions to the valid page span', () => {
    expect(fractionToPage(-0.5, 10)).toBe(0)
    expect(fractionToPage(1.5, 10)).toBe(9)
  })

  it('is 0 for an empty chapter', () => {
    expect(fractionToPage(0.5, 0)).toBe(0)
  })
})
