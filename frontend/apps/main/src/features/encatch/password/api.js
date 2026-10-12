// encatch: change your own password (cmd/encatch_password.go). Sends the CSRF token the
// same way as @main/api (csrf_token cookie -> X-CSRFTOKEN).
import axios from 'axios'

function csrfToken () {
  const match = document.cookie.split(';').map((c) => c.trim()).find((c) => c.startsWith('csrf_token='))
  return match ? match.substring('csrf_token='.length) : ''
}

export const changeOwnPassword = (currentPassword, newPassword) =>
  axios.put(
    '/api/v1/encatch/me/password',
    { current_password: currentPassword, new_password: newPassword },
    { timeout: 20000, headers: { 'Content-Type': 'application/json', 'X-CSRFTOKEN': csrfToken() } }
  )
