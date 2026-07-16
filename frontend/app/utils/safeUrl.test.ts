import { describe, it, expect } from 'vitest'
import { isHttpUrl, safeHttpUrl } from './safeUrl'

describe('isHttpUrl', () => {
  it('accepts http(s) URLs', () => {
    expect(isHttpUrl('https://example.com')).toBe(true)
    expect(isHttpUrl('http://example.com')).toBe(true)
  })
  it('rejects dangerous schemes', () => {
    expect(isHttpUrl('javascript:alert(1)')).toBe(false)
    expect(isHttpUrl('data:text/html,x')).toBe(false)
  })
})

describe('safeHttpUrl', () => {
  it('returns the URL for a safe absolute http(s) value', () => {
    expect(safeHttpUrl('https://asura.example/manga/1')).toBe('https://asura.example/manga/1')
  })
  it('returns undefined for empty, relative, or dangerous values', () => {
    expect(safeHttpUrl('')).toBeUndefined()
    expect(safeHttpUrl(null)).toBeUndefined()
    expect(safeHttpUrl(undefined)).toBeUndefined()
    expect(safeHttpUrl('/605z7-teach-me-first')).toBeUndefined()
    expect(safeHttpUrl('javascript:alert(1)')).toBeUndefined()
  })
})
