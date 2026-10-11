// Dates in IST and avatar initials, as the server-rendered pages show them
// (mailDate, date and initials in internal/encatch/mytickets/handlers.go).

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

const istParts = new Intl.DateTimeFormat('en-GB', {
  timeZone: 'Asia/Kolkata', year: 'numeric', month: 'numeric', day: 'numeric',
  hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
})

function ist(value) {
  const d = value instanceof Date ? value : new Date(value)
  const p = Object.fromEntries(istParts.formatToParts(d).map((x) => [x.type, x.value]))
  return { y: +p.year, m: +p.month, d: +p.day, time: `${p.hour}:${p.minute}` }
}

// mailDate: the time today, the day this year, else the full date (IST).
export function mailDate(value, now = new Date()) {
  const t = ist(value)
  const n = ist(now)
  if (t.y === n.y && t.m === n.m && t.d === n.d) return t.time
  if (t.y === n.y) return `${t.d} ${MONTHS[t.m - 1]}`
  return `${t.d} ${MONTHS[t.m - 1]} ${t.y}`
}

// longDate: "9 Oct 2026, 08:23" (IST).
export function longDate(value) {
  const t = ist(value)
  return `${t.d} ${MONTHS[t.m - 1]} ${t.y}, ${t.time}`
}

// initials: up to two letters, from the name's words, else the email.
export function initials(name, email = '') {
  let words = (name || '').trim().split(/\s+/).filter(Boolean)
  if (words.length === 0) words = [email || '']
  let out = ''
  for (const w of words) {
    const first = [...w][0]
    if (first) out += first.toUpperCase()
    if ([...out].length === 2) break
  }
  return out
}
