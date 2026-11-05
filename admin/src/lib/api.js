// Thin wrapper over the admin JSON API. The bearer token lives in
// sessionStorage: it dies with the tab, is not sent to any third-party origin,
// and never touches the bot's public server.
const TOKEN_KEY = 'rs8kvn.admin.token'

// Copy-paste from chat clients and PDFs drags in NBSP, zero-width joiners and
// smart quotes. fetch() rejects any header value outside ISO-8859-1, so junk is
// stripped at the boundary instead of surfacing as an opaque TypeError.
const INVISIBLE = /[\u00A0\u200B-\u200D\u2060\uFEFF]/g
const PRINTABLE_ASCII = /^[\x21-\x7E]+$/

export function normalizeToken(value) {
  return String(value ?? '').replace(INVISIBLE, '').trim()
}

export function isUsableToken(value) {
  return PRINTABLE_ASCII.test(value)
}

export function getToken() {
  return sessionStorage.getItem(TOKEN_KEY) || ''
}

export function setToken(token) {
  if (token) sessionStorage.setItem(TOKEN_KEY, token)
  else sessionStorage.removeItem(TOKEN_KEY)
}

export class ApiError extends Error {
  constructor(status, code, message) {
    super(message)
    this.status = status
    this.code = code
  }
}

async function request(method, path, body) {
  const headers = { Accept: 'application/json' }
  const token = normalizeToken(getToken())
  // A stale non-ASCII token from an earlier paste would make fetch() throw a
  // TypeError before any request leaves the tab. Treat it as "not signed in".
  if (token && !isUsableToken(token)) {
    setToken('')
    throw new ApiError(401, 'unauthorized', 'token contains non-ASCII characters')
  }
  if (token) headers.Authorization = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  let res
  try {
    res = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body)
    })
  } catch (err) {
    throw new ApiError(0, 'network', err?.message || 'network error')
  }

  const text = await res.text()
  let data = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = null
    }
  }

  if (!res.ok) {
    const code = data?.code || 'error'
    const message = data?.message || `HTTP ${res.status}`
    if (res.status === 401) setToken('')
    throw new ApiError(res.status, code, message)
  }

  return data
}

export const api = {
  checkSession: () => request('GET', '/admin/api/session'),
  listPlans: () => request('GET', '/admin/api/plans'),
  createPlan: (plan) => request('POST', '/admin/api/plans', plan),
  updatePlan: (id, plan) => request('PUT', `/admin/api/plans/${id}`, plan),
  deletePlan: (id) => request('DELETE', `/admin/api/plans/${id}`)
}
