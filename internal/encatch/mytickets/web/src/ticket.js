import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from './api'
import { orgSeen, state } from './session'

// useTicket loads the ticket in the URL and one page of its thread (?m=, 1 = newest),
// again whenever they or the current org change. A slower, older response never
// replaces a newer one.
export function useTicket() {
  const route = useRoute()
  const view = ref(null)
  const loading = ref(false)
  const error = ref(null) // { code, message }
  let seq = 0

  async function load() {
    const mine = ++seq
    const m = parseInt(route.query.m, 10)
    loading.value = true
    error.value = null
    try {
      const data = await api.ticket(String(route.params.ref), { m: m > 1 ? m : undefined })
      if (mine !== seq) return
      view.value = data
      // Opening a ticket from another of the user's orgs switches the session to it.
      await orgSeen(data.org_id)
    } catch (err) {
      if (mine === seq && err.code !== 'session_ended') {
        error.value = { code: err.code, message: err.message }
        view.value = null
      }
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  watch(() => [route.params.ref, route.query.m, state.orgVersion], load, { immediate: true })
  return { view, loading, error, reload: load }
}
