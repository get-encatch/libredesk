// New-ticket checks before sending, with the same rules and wording as the server
// (Create and checkNewTicket in internal/encatch/mytickets/handlers.go), which checks
// again. Lengths are counted in bytes, as the server counts them.

// ORG_LEVEL is the project choice for "Not about a specific project" (the select
// can't use an empty value).
export const ORG_LEVEL = 'org'

// byteLength counts as the server does (Go's len).
export const byteLength = (s) => new TextEncoder().encode(s).length

// newTicketProblem returns what's wrong with the form, or "". options is the
// server's new-ticket options; limits the session's text limits.
export function newTicketProblem({ subject, message, project }, options, limits) {
  let problem = ''
  if (!subject) problem = 'Please add a subject.'
  else if (byteLength(subject) > limits.subject) problem = `Please keep the subject under ${limits.subject} characters.`
  else if (!message) problem = 'Please describe the problem or question.'
  else if (byteLength(message) > limits.message) problem = `Please keep the message under ${limits.message} characters.`
  // A project problem wins, as on the server.
  const known = options.projects.some((p) => p.id === project)
  if ((!project && !options.allow_org_level) || (project && !known)) problem = 'Please choose one of your projects.'
  return problem
}

export const PRIORITIES = {
  Normal: 'Questions and issues with a workaround',
  Express: 'A key feature is broken, no workaround',
  Urgent: 'Production is down or data is at risk',
}
