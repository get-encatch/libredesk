import { watchEffect } from 'vue'

// useTitle keeps the tab title as "<page> · Encatch Support".
export function useTitle(get) {
  watchEffect(() => {
    const t = get()
    document.title = t ? `${t} · Encatch Support` : 'Encatch Support'
  })
}
