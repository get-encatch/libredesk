// The signed-in customer's session, shared by every view.
import { reactive } from 'vue'
import { api, sessionEnded, setCsrf } from './api'

export const state = reactive({
  // loading | ready | ended (session expired) | signed-out | error
  status: 'loading',
  session: null,
  message: '',
  // Bumped whenever the current org changes, so views reload their data.
  orgVersion: 0,
})

function use(session) {
  const orgChanged = state.session && state.session.org.id !== session.org.id
  state.session = session
  setCsrf(session.csrf)
  state.status = 'ready'
  if (orgChanged) state.orgVersion++
}

sessionEnded((err) => {
  state.status = 'ended'
  state.message = err.message
  setCsrf('')
})

export async function loadSession() {
  try {
    use(await api.session())
  } catch (err) {
    if (err.code !== 'session_ended') {
      state.status = 'error'
      state.message = err.message
    }
  }
}

// orgSeen keeps the session in step when the server switched org (opening a ticket
// from another of the user's orgs does that).
export async function orgSeen(orgID) {
  if (state.session && orgID && orgID !== state.session.org.id) await loadSession()
}

export async function switchOrg(id) {
  use(await api.switchOrg(id))
}

export async function signOut() {
  try {
    await api.logout()
  } catch (err) {
    if (err.code === 'session_ended') return
  }
  setCsrf('')
  state.session = null
  state.status = 'signed-out'
}
