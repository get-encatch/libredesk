// Light/dark: one toggle that flips what is shown. Matching the device again clears the
// saved choice, so the page simply follows the device. The cookie is the same one the
// server reads to render data-theme on <html> before any script runs.
import { ref } from 'vue'

export const COOKIE = 'libredesk_my_tickets_theme'

const device = typeof window !== 'undefined' && window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null

export function savedTheme(cookie = document.cookie) {
  const m = cookie.match(new RegExp(`(?:^|;\\s*)${COOKIE}=(light|dark)(?:;|$)`))
  return m ? m[1] : ''
}

// nextTheme returns the choice to save after a toggle ("" = follow the device).
export function nextTheme(saved, deviceDark) {
  const showingDark = saved ? saved === 'dark' : deviceDark
  const wantDark = !showingDark
  if (wantDark === deviceDark) return ''
  return wantDark ? 'dark' : 'light'
}

function apply(theme) {
  const root = document.documentElement
  if (theme) root.setAttribute('data-theme', theme)
  else root.removeAttribute('data-theme')
}

function showingDark() {
  const saved = document.documentElement.getAttribute('data-theme')
  return saved ? saved === 'dark' : !!device?.matches
}

export const isDark = ref(false)

// initTheme applies the saved choice (the server does too, once it serves this app)
// and follows device changes.
export function initTheme() {
  apply(savedTheme())
  isDark.value = showingDark()
  device?.addEventListener?.('change', () => {
    isDark.value = showingDark()
  })
}

export function toggleTheme() {
  const theme = nextTheme(document.documentElement.getAttribute('data-theme') || '', !!device?.matches)
  const secure = location.protocol === 'https:' ? '; Secure' : ''
  document.cookie = `${COOKIE}=${theme}; Path=/my-tickets; SameSite=Lax${secure}${theme ? '; Max-Age=31536000' : '; Max-Age=0'}`
  apply(theme)
  isDark.value = showingDark()
}
