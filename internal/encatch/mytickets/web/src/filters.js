// The inbox list's filters live in the URL (?status=&project=&q=&page=), like the
// server-rendered pages, so links and the back button keep the list as it was.

export const STATUS_OPEN = 'Open'
export const STATUS_WAITING = 'Waiting on you'
export const STATUS_RESOLVED = 'Resolved'
export const STATUSES = [STATUS_OPEN, STATUS_WAITING, STATUS_RESOLVED]

const one = (v) => (Array.isArray(v) ? v[0] : v) ?? ''

// filtersOf reads the filters from a route query.
export function filtersOf(q = {}) {
  const page = parseInt(one(q.page), 10)
  return {
    status: STATUSES.includes(one(q.status)) ? one(q.status) : '',
    project: String(one(q.project)),
    q: String(one(q.q)),
    page: page > 1 ? page : 1,
  }
}

// listQuery turns filters back into a route query, leaving out defaults.
export function listQuery({ status, project, q, page } = {}) {
  const out = {}
  if (status) out.status = status
  if (project) out.project = project
  if (q) out.q = q
  if (page > 1) out.page = String(page)
  return out
}
