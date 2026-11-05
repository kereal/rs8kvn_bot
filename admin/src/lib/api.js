// Thin wrapper over the admin JSON API. The bearer token lives in
// sessionStorage: it dies with the tab, is not sent to any third-party origin,
// and never touches the bot's public server.
const TOKEN_KEY = 'rs8kvn.admin.token'

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
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body)
  })

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
