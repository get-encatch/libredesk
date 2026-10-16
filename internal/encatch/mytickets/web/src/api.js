// The My Tickets JSON API (internal/encatch/mytickets/api.go). Successes are
// {"data": ...}; errors are {"error": {"code", "message"}}, where message is safe to
// show the customer. Every POST carries the session's CSRF token in a header.

export class ApiError extends Error {
  constructor(status, code, message) {
    super(message)
    this.status = status
    this.code = code
  }
}

const base = '/my-tickets/api'
let csrf = ''
let onSessionEnded = () => {}
let onStaleCSRF = () => {}

export function setCsrf(token) {
  csrf = token || ''
}

// sessionEnded registers what to do when any call finds the session gone.
export function sessionEnded(fn) {
  onSessionEnded = fn
}

// staleCSRF registers what to do when a change is refused for its CSRF token: the
// session cookie was replaced, e.g. by signing in again in another tab.
export function staleCSRF(fn) {
  onStaleCSRF = fn
}

// query drops empty values, so URLs stay clean.
export function query(params = {}) {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}

export async function request(method, path, { params, json, form } = {}) {
  const headers = { Accept: 'application/json' }
  let body
  if (method !== 'GET') headers['X-CSRF-Token'] = csrf
  if (json !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(json)
  } else if (form !== undefined) {
    body = form // FormData: the browser sets the multipart boundary
  }
  let res
  try {
    res = await fetch(base + path + query(params), { method, headers, body, credentials: 'same-origin' })
  } catch {
    throw new ApiError(0, 'network', "We couldn't reach Encatch Support. Check your connection and try again.")
  }
  let payload = null
  try {
    payload = await res.json()
  } catch {
    // A proxy error page, or a body too large for the server.
  }
  if (!res.ok || !payload || payload.error) {
    // A 429 comes from libredesk's rate limiter, in its own error format, not ours.
    const e = res.status === 429 ? { code: 'rate_limited', message: rateLimitedMessage(res) } : payload?.error ?? {
      code: res.status === 413 ? 'too_large' : 'server',
      message: res.status === 413 ? 'Those files are too large to send. Please attach fewer or smaller files.' : 'Something went wrong. Please try again.',
    }
    const err = new ApiError(res.status, e.code, e.message)
    if (err.code === 'session_ended') onSessionEnded(err)
    if (err.code === 'csrf') onStaleCSRF(err)
    throw err
  }
  return payload.data
}

// rateLimitedMessage explains a 429 (ratelimit.go, or libredesk's per-address limit);
// Retry-After says when to try again.
function rateLimitedMessage(res) {
  const secs = parseInt(res.headers?.get?.('Retry-After') ?? '', 10)
  const wait = secs > 0 && secs <= 120 ? `${secs} seconds` : 'a minute'
  return `Too many requests right now. Please wait ${wait} and try again; what you've written is kept.`
}

export const api = {
  session: () => request('GET', '/session'),
  switchOrg: (org) => request('POST', '/org', { json: { org } }),
  logout: () => request('POST', '/logout'),
  tickets: (params) => request('GET', '/tickets', { params }),
  ticket: (ref, params) => request('GET', `/tickets/${encodeURIComponent(ref)}`, { params }),
  newTicket: () => request('GET', '/new-ticket'),
  createTicket: (form) => request('POST', '/tickets', { form }),
  reply: (ref, form) => request('POST', `/tickets/${encodeURIComponent(ref)}/replies`, { form }),
}
