import { describe, it, expect } from 'vitest'
import { isHttpUrl } from './safeUrl'

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
