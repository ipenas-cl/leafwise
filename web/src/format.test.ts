import { describe, expect, it } from 'vitest'
import { dryness, groupByDay, percent, relativeDay } from './format'
import type { CalendarEntry } from './api'

const now = new Date('2026-10-22T15:00:00Z')

describe('relativeDay', () => {
  it('speaks human', () => {
    expect(relativeDay('2026-10-22T00:00:00Z', now)).toBe('today')
    expect(relativeDay('2026-10-23T00:00:00Z', now)).toBe('tomorrow')
    expect(relativeDay('2026-10-27T00:00:00Z', now)).toBe('in 5 days')
    expect(relativeDay('2026-10-20T00:00:00Z', now)).toBe('2 days ago')
  })
})

describe('groupByDay', () => {
  it('groups entries by date in order', () => {
    const e = (due: string, plantName: string) =>
      ({ due, plantName, kind: 'water', plantId: '1', overdue: false, reason: '', species: '' }) as CalendarEntry
    const g = groupByDay([e('2026-10-22T00:00:00Z', 'a'), e('2026-10-22T00:00:00Z', 'b'), e('2026-10-24T00:00:00Z', 'c')])
    expect(g.map(([d, xs]) => [d, xs.length])).toEqual([
      ['2026-10-22', 2],
      ['2026-10-24', 1],
    ])
  })
})

describe('helpers', () => {
  it('clamps dryness and formats percent', () => {
    expect(dryness(30, 20)).toBe(1)
    expect(dryness(5, 20)).toBe(0.25)
    expect(dryness(5, 0)).toBe(0)
    expect(percent(0.784)).toBe('78%')
  })
})
