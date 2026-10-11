import { describe, expect, it } from 'vitest'
import { newTicketProblem } from './newTicket'

const limits = { subject: 200, message: 20000 }
const manager = { allow_org_level: true, projects: [{ id: 'prod-17', name: 'Mobile app' }] }
const projectOnly = { allow_org_level: false, projects: [{ id: 'prod-17', name: 'Mobile app' }] }
const ok = { subject: 'Checkout broken', message: 'Steps', project: '' }

// The same rules as Create in handlers.go (and TestFlow / TestAccess).
describe('newTicketProblem', () => {
  it('accepts a complete ticket', () => {
    expect(newTicketProblem(ok, manager, limits)).toBe('')
    expect(newTicketProblem({ ...ok, project: 'prod-17' }, projectOnly, limits)).toBe('')
  })
  it.each([
    [{ subject: '' }, 'Please add a subject.'],
    [{ subject: 'a'.repeat(201) }, 'Please keep the subject under 200 characters.'],
    [{ subject: 'é'.repeat(101) }, 'Please keep the subject under 200 characters.'], // 202 bytes, as the server counts
    [{ message: '' }, 'Please describe the problem or question.'],
    [{ message: 'a'.repeat(20001) }, 'Please keep the message under 20000 characters.'],
    [{ project: 'prod-99' }, 'Please choose one of your projects.'],
  ])('%j → %s', (change, want) => {
    expect(newTicketProblem({ ...ok, ...change }, manager, limits)).toBe(want)
  })
  it('requires a project from project-only users, over other problems', () => {
    expect(newTicketProblem(ok, projectOnly, limits)).toBe('Please choose one of your projects.')
    expect(newTicketProblem({ ...ok, subject: '' }, projectOnly, limits)).toBe('Please choose one of your projects.')
  })
})
