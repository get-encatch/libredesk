import { describe, expect, it } from 'vitest'
import { linkHref } from './richText'

describe('linkHref', () => {
  it.each([
    ['https://encatch.com/docs', 'https://encatch.com/docs'],
    ['http://example.com', 'http://example.com'],
    ['encatch.com/pricing', 'https://encatch.com/pricing'],
    ['  docs.encatch.com  ', 'https://docs.encatch.com'],
    ['support@encatch.com', 'mailto:support@encatch.com'],
    ['mailto:support@encatch.com', 'mailto:support@encatch.com'],
  ])('%s → %s', (raw, want) => expect(linkHref(raw)).toBe(want))

  it.each(['', 'javascript:alert(1)', 'JavaScript:alert(1)', 'data:text/html,x', 'ftp://files.example.com', 'not a link', 'mailto:nobody', 'localhost:3000x y', 'word'])(
    'refuses %j', (raw) => expect(linkHref(raw)).toBe(''),
  )
})
