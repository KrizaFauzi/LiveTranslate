export class AuthError extends Error {
  constructor(message, status = 0) { super(message); this.status = status }
}
export function createSessionStorage(chromeApi = globalThis.chrome) {
  let memory = null
  const session = chromeApi?.storage?.session
  const ready = session?.setAccessLevel({ accessLevel: 'TRUSTED_CONTEXTS' }) ?? Promise.resolve()
  return {
    async read() { await ready; return session ? (await session.get('auth_session')).auth_session ?? null : memory },
    async write(value) { await ready; if (session) await session.set({ auth_session: value }); else memory = value },
    async clear() { await ready; if (session) await session.remove('auth_session'); else memory = null },
  }
}
function validSession(value) {
  return value && typeof value.access_token === 'string' && value.access_token && typeof value.refresh_token === 'string' && value.refresh_token && Number.isFinite(value.expires_at) && value.token_type === 'bearer'
}
export function createAuthClient({ fetchImpl = globalThis.fetch, storage = createSessionStorage(), baseUrl = '', now = Date.now } = {}) {
  const base = baseUrl.replace(/\/$/, '')
  let restorePending = null
  let epoch = 0
  async function call(path, body, accessToken) {
    const headers = {}
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    if (accessToken) headers.Authorization = `Bearer ${accessToken}`
    let response
    try { response = await fetchImpl(`${base}/api/auth/${path}`, {
      method: body !== undefined || path === 'logout' ? 'POST' : 'GET', headers,
      body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(10000), cache: 'no-store',
    }) } catch { throw new AuthError('Tidak dapat menghubungi backend. Periksa koneksi lalu coba lagi.') }
    let data = null
    if (response.status !== 204) {
      try { data = await response.json() } catch { throw new AuthError('Respons backend tidak valid.', response.status) }
    }
    if (!response.ok) throw new AuthError(data?.error?.message || 'Permintaan gagal.', response.status)
    return data
  }
  async function save(value, started = epoch) {
    if (!validSession(value)) throw new AuthError('Respons sesi tidak valid.')
    if (started !== epoch) throw new AuthError('Sesi sudah berubah. Silakan masuk kembali.')
    const { access_token, refresh_token, expires_at, token_type } = value
    await storage.write({ access_token, refresh_token, expires_at, token_type })
  }
  async function refresh(session, started) {
    try {
      const data = await call('refresh', { refresh_token: session.refresh_token })
      await save(data.session, started)
      return data.session
    } catch (error) {
      if (error.status === 401 && started === epoch) await storage.clear()
      throw error
    }
  }
  async function restore() {
    if (restorePending) return restorePending
    const started = epoch
    restorePending = (async () => {
      let session = await storage.read()
      if (!session) return null
      if (!validSession(session)) { await storage.clear(); return null }
      let refreshed = false
      if (session.expires_at <= now() / 1000 + 60) { session = await refresh(session, started); refreshed = true }
      try { return await call('me', undefined, session.access_token) } catch (error) {
        if (error.status !== 401) throw error
        if (!refreshed) {
          session = await refresh(session, started)
          try { return await call('me', undefined, session.access_token) } catch (retryError) {
            if (retryError.status === 401 && started === epoch) await storage.clear()
            throw retryError
          }
        }
        if (started === epoch) await storage.clear()
        throw error
      }
    })().finally(() => { restorePending = null })
    return restorePending
  }
  return {
    restore,
    async login(input) { const started = ++epoch; const data = await call('login', input); await save(data.session, started); return restore() },
    async register(input) {
      const started = ++epoch, data = await call('register', input)
      if (data.session) { await save(data.session, started); return { ...data, account: await restore() } }
      return data
    },
    async logout() {
      ++epoch
      const session = await storage.read()
      await storage.clear()
      if (session?.access_token) {
        try { await call('logout', undefined, session.access_token) } catch {
          throw new AuthError('Sesi lokal dihapus. Logout di server belum terkonfirmasi.')
        }
      }
    },
  }
}
