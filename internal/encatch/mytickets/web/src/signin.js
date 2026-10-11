// A failed sign-in redirects to /my-tickets?signin=<reason> (signInFailed in
// internal/encatch/mytickets/handlers.go). Unknown reasons are ignored.
export const SIGNIN_PROBLEMS = {
  missing: 'This link is missing its sign-in token. Open Support again from your Encatch app.',
  invalid: 'This sign-in link is invalid or has expired. Open Support again from your Encatch app.',
  used: 'This sign-in link has already been used. Open Support again from your Encatch app.',
  error: 'Something went wrong. Please try again.',
}

export function signInProblem(query) {
  const reason = Array.isArray(query.signin) ? query.signin[0] : query.signin
  return Object.hasOwn(SIGNIN_PROBLEMS, reason) ? SIGNIN_PROBLEMS[reason] : ''
}
