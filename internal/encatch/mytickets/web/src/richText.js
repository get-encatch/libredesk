// Link addresses from the editor's link box: web addresses (https:// added when
// missing) and email addresses. Anything else ("javascript:", spaces) is refused,
// and the server allows only http, https and mailto links anyway.

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function linkHref(raw) {
  const s = (raw || '').trim()
  if (!s || /\s/.test(s)) return ''
  if (/^mailto:/i.test(s)) return EMAIL.test(s.slice(7)) ? s : ''
  if (EMAIL.test(s)) return `mailto:${s}`
  const withScheme = /^[a-z][a-z0-9+.-]*:/i.test(s) ? s : `https://${s}`
  let url
  try {
    url = new URL(withScheme)
  } catch {
    return ''
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return ''
  if (!url.hostname.includes('.') && url.hostname !== 'localhost') return ''
  return withScheme
}
