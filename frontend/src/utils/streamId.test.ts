import { describe, it, expect } from 'vitest'
import { compareStreamIds } from './streamId'

describe('compareStreamIds (Redis entry ids, numeric)', () => {
  it('orders numerically by ms then seq — never lexicographically', () => {
    expect(compareStreamIds('1-0', '2-0')).toBeLessThan(0)
    expect(compareStreamIds('9-0', '10-0')).toBeLessThan(0)   // "9" > "10" as strings
    expect(compareStreamIds('10-0', '9-99')).toBeGreaterThan(0)
    expect(compareStreamIds('5-1', '5-2')).toBeLessThan(0)
    expect(compareStreamIds('5-2', '5-2')).toBe(0)
    expect(compareStreamIds('100-5', '101-0')).toBeLessThan(0)
  })
})
