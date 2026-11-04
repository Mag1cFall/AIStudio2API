import { describe, expect, it } from 'vite-plus/test'
import { groupCooldowns, pacificDayStart } from './overview-metrics'

describe('overview metrics', () => {
  it('groups distinct cooling accounts and their earliest recovery by model', () => {
    expect(
      groupCooldowns([
        { account_id: 'a', model_id: 'argon', until: '2026-11-02T08:00:00Z' },
        { account_id: 'b', model_id: 'argon', until: '2026-11-02T07:00:00Z' },
        { account_id: 'b', model_id: 'argon', until: '2026-11-02T09:00:00Z' },
        { account_id: 'a', model_id: '', until: '2026-11-02T06:00:00Z' },
      ]),
    ).toEqual([
      { model: 'argon', accounts: 2, until: '2026-11-02T07:00:00Z' },
      { model: '*', accounts: 1, until: '2026-11-02T06:00:00Z' },
    ])
    expect(groupCooldowns([])).toEqual([])
  })

  it.each([
    ['2026-11-01T06:59:59Z', '2026-10-31T07:00:00.000Z'],
    ['2026-11-01T07:00:00Z', '2026-11-01T07:00:00.000Z'],
    ['2026-11-01T20:00:00Z', '2026-11-01T07:00:00.000Z'],
    ['2026-11-02T20:00:00Z', '2026-11-02T08:00:00.000Z'],
    ['2026-03-08T07:59:59Z', '2026-03-07T08:00:00.000Z'],
    ['2026-03-08T20:00:00Z', '2026-03-08T08:00:00.000Z'],
    ['2026-03-09T20:00:00Z', '2026-03-09T07:00:00.000Z'],
  ])('uses the Pacific midnight offset for %s', (now, expected) => {
    expect(pacificDayStart(new Date(now)).toISOString()).toBe(expected)
  })
})
