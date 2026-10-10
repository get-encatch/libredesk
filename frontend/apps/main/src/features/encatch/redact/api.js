// encatch: API calls for removing sensitive content (cmd/encatch_redact.go).
// Sends the CSRF token the same way as @main/api (csrf_token cookie -> X-CSRFTOKEN).
import axios from 'axios'

function csrfToken () {
  const match = document.cookie.split(';').map((c) => c.trim()).find((c) => c.startsWith('csrf_token='))
  return match ? match.substring('csrf_token='.length) : ''
}

const post = (path, body) =>
  axios.post(path, body, {
    timeout: 20000,
    headers: { 'Content-Type': 'application/json', 'X-CSRFTOKEN': csrfToken() }
  })

const base = (cuuid, uuid) => `/api/v1/encatch/conversations/${cuuid}/messages/${uuid}`

export const removeAttachment = (cuuid, uuid, mediaUUID, reason) =>
  post(`${base(cuuid, uuid)}/remove-attachment`, { media_uuid: mediaUUID, reason })

export const removeText = (cuuid, uuid, reason) =>
  post(`${base(cuuid, uuid)}/remove-text`, { reason })
