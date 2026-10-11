import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from './api'
import { filtersOf, listQuery } from './filters'
import { state } from './session'

// useTickets loads the inbox list for the filters in the URL, again whenever they or
// the current org change. A slower, older response never replaces a newer one.
export function useTickets() {
  const route = useRoute()
  const list = ref(null)
  const loading = ref(false)
  const error = ref('')
  let seq = 0

  async function load() {
    const mine = ++seq
    loading.value = true
    error.value = ''
    try {
      const data = await api.tickets(listQuery(filtersOf(route.query)))
      if (mine === seq) list.value = data
    } catch (err) {
      if (mine === seq && err.code !== 'session_ended') error.value = err.message
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  watch(
    () => [route.query.status, route.query.project, route.query.q, route.query.page, state.orgVersion],
    load,
    { immediate: true },
  )
  return { list, loading, error, reload: load }
}
