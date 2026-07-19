/**
 * useErrorDiagnosis — the human diagnosis engine.
 *
 * Pins that every known error category maps to its own title + non-empty
 * suggestions, that an absent/unmapped category falls back to the "unknown"
 * diagnosis, and that the badge label comes from the shared errorCategory
 * metadata.
 *
 * Non-vacuous: swap a category's title in the map and its assertion fails; drop
 * the unknown fallback and the null/garbage cases throw.
 */
import { describe, it, expect } from 'vitest'
import { useErrorDiagnosis } from './useErrorDiagnosis'

const { diagnose } = useErrorDiagnosis()

describe('useErrorDiagnosis', () => {
  it('maps each known category to a distinct, actionable diagnosis', () => {
    const cases: Record<string, string> = {
      captcha: 'Blocked by anti-bot protection',
      rate_limit: 'Rate limited by the source',
      not_found: 'Resource not found',
      server_error: 'Source returned a server error',
      network: 'Network failure',
      timeout: 'Request timed out',
      parse: 'Response could not be read',
      no_pages: 'No readable pages',
    }
    for (const [category, title] of Object.entries(cases)) {
      const d = diagnose(category, 'some raw message')
      expect(d.category).toBe(category)
      expect(d.title).toBe(title)
      expect(d.suggestions.length).toBeGreaterThan(0)
      expect(d.explanation.length).toBeGreaterThan(0)
    }
  })

  it('carries the shared category label', () => {
    expect(diagnose('captcha', '').categoryLabel).toBe('Anti-bot')
    expect(diagnose('rate_limit', '').categoryLabel).toBe('Rate limited')
  })

  it('falls back to the unknown diagnosis for an absent category', () => {
    const d = diagnose(null, 'weird failure')
    expect(d.category).toBe('unknown')
    expect(d.title).toBe('Unclassified error')
    expect(d.suggestions.length).toBeGreaterThan(0)
  })

  it('falls back to unknown for an unmapped category string', () => {
    const d = diagnose('brand_new_category', 'x')
    expect(d.category).toBe('unknown')
    expect(d.title).toBe('Unclassified error')
    // The label still reflects the fallback treatment.
    expect(d.categoryLabel).toBe('Unknown')
  })
})
