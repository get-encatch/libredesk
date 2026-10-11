import { describe, expect, it } from 'vitest'
import { filtersOf, listQuery } from './filters'

describe('filtersOf', () => {
  it('reads valid filters', () => {
    expect(filtersOf({ status: 'Waiting on you', project: 'prod-17', q: 'crash', page: '3' }))
      .toEqual({ status: 'Waiting on you', project: 'prod-17', q: 'crash', page: 3 })
  })
  it('drops unknown statuses and bad pages', () => {
    expect(filtersOf({ status: 'Waiting on engineering', page: 'abc' })).toEqual({ status: '', project: '', q: '', page: 1 })
    expect(filtersOf({ page: '-2' }).page).toBe(1)
    expect(filtersOf({ status: ['Open', 'Resolved'] }).status).toBe('Open')
  })
})

describe('listQuery', () => {
  it('leaves out defaults', () => {
    expect(listQuery({ status: '', project: '', q: '', page: 1 })).toEqual({})
    expect(listQuery({ status: 'Open', project: 'prod-17', q: 'x', page: 2 })).toEqual({ status: 'Open', project: 'prod-17', q: 'x', page: '2' })
  })
  it('round-trips', () => {
    const f = { status: 'Resolved', project: 'prod-18', q: 'csv export', page: 4 }
    expect(filtersOf(listQuery(f))).toEqual(f)
  })
})
