import { describe, expect, it } from 'vitest'
import { signInProblem } from './signin'

describe('signInProblem', () => {
  it('explains each reason the server sends', () => {
    expect(signInProblem({ signin: 'used' })).toMatch(/already been used/)
    expect(signInProblem({ signin: 'invalid' })).toMatch(/invalid or has expired/)
    expect(signInProblem({ signin: 'missing' })).toMatch(/missing its sign-in token/)
    expect(signInProblem({ signin: 'error' })).toBe('Something went wrong. Please try again.')
  })
  it('ignores anything else', () => {
    expect(signInProblem({})).toBe('')
    expect(signInProblem({ signin: '<b>hi</b>' })).toBe('')
    expect(signInProblem({ signin: 'toString' })).toBe('')
    expect(signInProblem({ signin: ['used', 'x'] })).toMatch(/already been used/)
  })
})
