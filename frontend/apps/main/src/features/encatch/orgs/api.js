// encatch: Customer organisations API (cmd/encatch_orgs.go). Sends the CSRF token the
// same way as @main/api (csrf_token cookie -> X-CSRFTOKEN).
import axios from 'axios'

function csrfToken () {
  const match = document.cookie.split(';').map((c) => c.trim()).find((c) => c.startsWith('csrf_token='))
  return match ? match.substring('csrf_token='.length) : ''
}

const opts = (extra = {}) => ({
  timeout: 20000,
  ...extra,
  headers: { 'Content-Type': 'application/json', 'X-CSRFTOKEN': csrfToken(), ...(extra.headers || {}) }
})

export const listOrgs = (params) => axios.get('/api/v1/encatch/orgs', opts({ params }))
export const getOrgsMeta = () => axios.get('/api/v1/encatch/orgs/meta', opts())
export const setOrgTier = (orgId, tier) =>
  axios.put(`/api/v1/encatch/orgs/${encodeURIComponent(orgId)}/tier`, { tier }, opts())
export const addOrg = (body) => axios.post('/api/v1/encatch/orgs', body, opts())
export const getOrgTierChanges = (orgId) =>
  axios.get(`/api/v1/encatch/orgs/${encodeURIComponent(orgId)}/changes`, opts())
