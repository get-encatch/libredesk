// encatch: the form's password rules must match the server's (internal/user/user.go IsStrongPassword).
import { describe, expect, it } from 'vitest'
import { isStrongPassword, passwordRules } from './rules'

describe('password rules', () => {
  it('accepts a password meeting every rule', () => {
    expect(isStrongPassword('Brand-new-pass-2')).toBe(true)
    expect(isStrongPassword('Abcdefgh1_')).toBe(true) // underscore counts as special, as on the server
  })

  it.each([
    ['too short', 'Ab1-'],
    ['too long', 'Aa1-' + 'x'.repeat(69)],
    ['no uppercase', 'brand-new-pass-2'],
    ['no lowercase', 'BRAND-NEW-PASS-2'],
    ['no number', 'Brand-new-pass'],
    ['no special character', 'Brandnewpass2']
  ])('rejects a password with %s', (_, p) => {
    expect(isStrongPassword(p)).toBe(false)
  })

  it('reports each rule separately', () => {
    expect(passwordRules.map((r) => r.test('abc'))).toEqual([false, false, true, false, false])
  })
})
