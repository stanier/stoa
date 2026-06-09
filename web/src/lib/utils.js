export function formatDate(iso) {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

function getCsrfToken() {
  return document.cookie
    .split(';')
    .map(c => c.trim())
    .find(c => c.startsWith('__csrf='))
    ?.split('=')[1] ?? ''
}

const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])

// apiFetch wraps fetch() and automatically adds the X-CSRF-Token header for
// mutating requests (POST, PUT, DELETE, PATCH), reading the token from the
// __csrf double-submit cookie set at login.
export async function apiFetch(url, options = {}) {
  const method = (options.method ?? 'GET').toUpperCase()
  const headers = new Headers(options.headers ?? {})
  if (!SAFE_METHODS.has(method)) {
    const token = getCsrfToken()
    if (token) headers.set('X-CSRF-Token', token)
  }
  return fetch(url, { ...options, headers })
}
