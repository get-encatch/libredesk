import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { COOKIE, nextTheme, savedTheme } from './theme'

describe('nextTheme', () => {
  // Following the device (no saved choice) flips to the other theme; flipping back to
  // what the device shows clears the choice.
  it.each([
    ['', false, 'dark'],
    ['', true, 'light'],
    ['dark', false, ''],
    ['light', true, ''],
    ['dark', true, 'light'],
    ['light', false, 'dark'],
  ])('saved %j, device dark %s → %j', (saved, deviceDark, want) => {
    expect(nextTheme(saved, deviceDark)).toBe(want)
  })
})

describe('savedTheme', () => {
  it('reads only light or dark', () => {
    expect(savedTheme(`a=1; ${COOKIE}=dark; b=2`)).toBe('dark')
    expect(savedTheme(`${COOKIE}=light`)).toBe('light')
    expect(savedTheme(`${COOKIE}=purple`)).toBe('')
    expect(savedTheme(`x${COOKIE}=dark`)).toBe('')
    expect(savedTheme('')).toBe('')
  })
})

// The dark tokens appear twice in app.css (chosen Dark, and a dark device without a
// Light choice); they must not drift apart.
describe('app.css', () => {
  // Vitest stubs CSS imports, so read the files (tests run from web/).
  const css = readFileSync('src/app.css', 'utf8')
  const old = readFileSync('../ui/app.css', 'utf8')
  const block = (selector) => {
    const i = css.indexOf(`${selector} {`)
    expect(i).toBeGreaterThan(-1)
    const body = css.slice(css.indexOf('{', i) + 1)
    return body.slice(0, body.indexOf('}')).split(/\s+/).filter(Boolean).join(' ')
  }
  it('keeps both dark token blocks identical', () => {
    const chosen = block(':root[data-theme="dark"]')
    const device = block(':root:not([data-theme="light"])')
    expect(chosen).toContain('--background:')
    expect(device).toBe(chosen)
  })
  it('uses the same tokens as the server-rendered pages', () => {
    for (const token of ['--background: #ffffff', '--primary: #394dfe', '--background: #09090b', '--muted-foreground: #9aa4b8']) {
      expect(old).toContain(token)
      expect(css).toContain(token)
    }
  })
})
