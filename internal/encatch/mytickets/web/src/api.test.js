import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, query, request, sessionEnded, setCsrf, staleCSRF } from './api'

function respond(status, body) {
  return vi.fn(async () => ({
    ok: status >= 200 && status < 300,
    status,
    json: async () => {
      if (body === undefined) throw new SyntaxError('not json')
      return body
    },
  }))
}

afterEach(() => {
  vi.unstubAllGlobals()
  setCsrf('')
  sessionEnded(() => {})
  staleCSRF(() => {})
})

describe('request', () => {
  it('returns data and sends the CSRF header only on changes', async () => {
    const fetch = respond(200, { data: { ok: 1 } })
    vi.stubGlobal('fetch', fetch)
    setCsrf('tok')
    expect(await api.tickets({ status: 'Open', q: '' })).toEqual({ ok: 1 })
    expect(fetch.mock.calls[0][0]).toBe('/my-tickets/api/tickets?status=Open')
    expect(fetch.mock.calls[0][1].headers['X-CSRF-Token']).toBeUndefined()
    await api.switchOrg('prod-42')
    const [url, init] = fetch.mock.calls[1]
    expect(url).toBe('/my-tickets/api/org')
    expect(init.method).toBe('POST')
    expect(init.headers['X-CSRF-Token']).toBe('tok')
    expect(init.headers['Content-Type']).toBe('application/json')
    expect(JSON.parse(init.body)).toEqual({ org: 'prod-42' })
  })

  it('escapes ticket refs', async () => {
    const fetch = respond(200, { data: {} })
    vi.stubGlobal('fetch', fetch)
    await api.ticket('1/2', { m: 2 })
    expect(fetch.mock.calls[0][0]).toBe('/my-tickets/api/tickets/1%2F2?m=2')
  })

  it('throws the server message', async () => {
    vi.stubGlobal('fetch', respond(422, { error: { code: 'invalid', message: 'Please add a subject.' } }))
    await expect(request('POST', '/tickets')).rejects.toMatchObject({ status: 422, code: 'invalid', message: 'Please add a subject.' })
  })

  it('explains a rate limit, whatever the error format', async () => {
    const fetch = vi.fn(async () => ({
      ok: false,
      status: 429,
      headers: new Headers({ 'Retry-After': '60' }),
      json: async () => ({ status: 'error', message: 'Rate limit exceeded' }),
    }))
    vi.stubGlobal('fetch', fetch)
    await expect(request('POST', '/tickets/112/replies')).rejects.toMatchObject({
      status: 429,
      code: 'rate_limited',
      message: expect.stringContaining('Please wait 60 seconds and try again'),
    })
  })

  it('falls back to a minute without a usable Retry-After', async () => {
    vi.stubGlobal('fetch', respond(429, { status: 'error', message: 'Rate limit exceeded' }))
    await expect(request('GET', '/tickets')).rejects.toMatchObject({
      code: 'rate_limited',
      message: expect.stringContaining('Please wait a minute and try again'),
    })
  })

  it('reports an ended session', async () => {
    vi.stubGlobal('fetch', respond(401, { error: { code: 'session_ended', message: 'Gone' } }))
    const ended = vi.fn()
    sessionEnded(ended)
    await expect(api.session()).rejects.toBeInstanceOf(ApiError)
    expect(ended).toHaveBeenCalledWith(expect.objectContaining({ code: 'session_ended', message: 'Gone' }))
  })

  it('reports a stale CSRF token', async () => {
    vi.stubGlobal('fetch', respond(403, { error: { code: 'csrf', message: 'Your page expired. Please reload and try again.' } }))
    const stale = vi.fn()
    staleCSRF(stale)
    await expect(request('POST', '/org')).rejects.toMatchObject({ code: 'csrf' })
    expect(stale).toHaveBeenCalledTimes(1)
  })

  it('explains bodies the proxy refused and non-JSON errors', async () => {
    vi.stubGlobal('fetch', respond(413))
    await expect(request('POST', '/tickets')).rejects.toMatchObject({ code: 'too_large' })
    vi.stubGlobal('fetch', respond(502))
    await expect(request('GET', '/tickets')).rejects.toMatchObject({ code: 'server', message: 'Something went wrong. Please try again.' })
  })

  it('explains network failures', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch') }))
    await expect(request('GET', '/session')).rejects.toMatchObject({ status: 0, code: 'network' })
  })
})

describe('query', () => {
  it('drops empty values', () => {
    expect(query({ a: '', b: null, c: undefined, d: 0, e: 'x y' })).toBe('?d=0&e=x+y')
    expect(query({})).toBe('')
  })
})
