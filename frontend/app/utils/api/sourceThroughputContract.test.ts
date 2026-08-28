import { describe, expect, it } from 'vitest'

import type { operations } from './schema'

type UpdatePath = operations['updateSourceThroughput']['parameters']['path']

describe('source throughput API identity contract', () => {
  it('keeps signed int64 source ids as exact path strings', () => {
    const jsUnsafeSourceId: UpdatePath['sourceId'] = '1998416842837112832'
    const negativeSourceId: UpdatePath['sourceId'] = '-42'

    expect(jsUnsafeSourceId).toBe('1998416842837112832')
    expect(negativeSourceId).toBe('-42')
  })
})
