import { describe, expect, it } from 'vitest'
import { initials, longDate, mailDate } from './format'

// The same cases as TestMailDate and TestInitials in mytickets_test.go.
describe('mailDate', () => {
  const now = new Date('2026-10-10T09:00:00+05:30')
  it.each([
    ['2026-10-10T08:05:00+05:30', '08:05'],
    ['2026-10-09T23:00:00+05:30', '9 Oct'],
    ['2026-10-09T19:00:00Z', '00:30'], // 00:30 IST on 10 Oct: today in IST
    ['2025-12-31T12:00:00+05:30', '31 Dec 2025'],
    ['2026-09-03T10:00:00+05:30', '3 Sep'], // "Sep", not the ICU "Sept"
  ])('%s → %s', (t, want) => {
    expect(mailDate(t, now)).toBe(want)
  })
})

describe('longDate', () => {
  it('formats in IST', () => {
    expect(longDate('2026-10-09T02:53:25Z')).toBe('9 Oct 2026, 08:23')
  })
})

describe('initials', () => {
  it.each([
    ['Anita Rao', 'a@x.com', 'AR'],
    ['ravi', 'r@x.com', 'R'],
    ['Anne Marie Smith', '', 'AM'],
    ['', 'meera@x.com', 'M'],
    ['  ', '', ''],
    ['élodie durand', '', 'ÉD'],
    ['Encatch Support', '', 'ES'],
    ['You', '', 'Y'],
  ])('%j %j → %j', (name, email, want) => {
    expect(initials(name, email)).toBe(want)
  })
})
