// encatch: the server's password rules (internal/user/user.go IsStrongPassword), checked
// in the form first so agents see what's missing before sending.
export const MIN_LENGTH = 10
export const MAX_LENGTH = 72

export const passwordRules = [
  { label: `${MIN_LENGTH}–${MAX_LENGTH} characters`, test: (p) => p.length >= MIN_LENGTH && p.length <= MAX_LENGTH },
  { label: 'An uppercase letter', test: (p) => /[A-Z]/.test(p) },
  { label: 'A lowercase letter', test: (p) => /[a-z]/.test(p) },
  { label: 'A number', test: (p) => /[0-9]/.test(p) },
  { label: 'A special character', test: (p) => /[\W_]/.test(p) }
]

export const isStrongPassword = (p) => passwordRules.every((r) => r.test(p))
