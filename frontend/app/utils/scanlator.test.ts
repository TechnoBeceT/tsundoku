import { describe, it, expect } from 'vitest'
import { collapseUntaggedScanlator } from './scanlator'

describe('collapseUntaggedScanlator', () => {
  it('collapses an exact source-name match to ""', () => {
    expect(collapseUntaggedScanlator('MangaDex', 'MangaDex')).toBe('')
  })

  it('collapses case-insensitively', () => {
    expect(collapseUntaggedScanlator('mangadex', 'MangaDex')).toBe('')
    expect(collapseUntaggedScanlator('COMIX', 'comix')).toBe('')
  })

  it('collapses ignoring surrounding whitespace', () => {
    expect(collapseUntaggedScanlator('  Asura Scans ', 'Asura Scans')).toBe('')
  })

  it('keeps a real scanlator verbatim', () => {
    expect(collapseUntaggedScanlator('Reset Scans', 'MangaDex')).toBe('Reset Scans')
  })

  it('keeps an already-empty scanlator empty', () => {
    expect(collapseUntaggedScanlator('', 'MangaDex')).toBe('')
  })
})
