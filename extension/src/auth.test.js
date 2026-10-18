import test from 'node:test'
import assert from 'node:assert/strict'
import { createAuthClient } from './auth.js'
const session = { access_token: 'old', refresh_token: 'refresh-old', expires_at: 2000, token_type: 'bearer' }
const fresh = { access_token: 'new', refresh_token: 'refresh-new', expires_at: 4000, token_type: 'bearer' }
function store(initial = null) { let value = initial; return { read: async () => value, write: async s => { value = s }, clear: async () => { value = null } } }
const json = (value, status = 200) => new Response(JSON.stringify(value), { status })
test('login stores session and reload fetches account', async () => {
 const storage = store()
 const client = createAuthClient({ storage, now: () => 1000000, baseUrl: 'http://api', fetchImpl: async url => url.endsWith('/login') ? json({ session }) : json({ user: { id: 'a' }, profile: { display_name: 'A' } }) })
 await client.login({ email: 'a@example.com', password: 'password1' })
 assert.deepEqual(await storage.read(), session)
 assert.equal((await client.restore()).profile.display_name, 'A')
})
test('concurrent restore refreshes once and persists rotated tokens', async () => {
 let refreshes = 0
 const storage = store({ ...session, expires_at: 1010 })
 const client = createAuthClient({ storage, now: () => 1000000, fetchImpl: async (url, options) => {
  if (url.endsWith('/refresh')) { refreshes++; await new Promise(resolve => setTimeout(resolve, 5)); return json({ session: fresh }) }
  assert.equal(options.headers.Authorization, 'Bearer new')
  assert.deepEqual(await storage.read(), fresh)
  return json({ user: { id: 'a' }, profile: { display_name: 'A' } })
 } })
 await Promise.all([client.restore(), client.restore()])
 assert.equal(refreshes, 1)
 assert.deepEqual(await storage.read(), fresh)
})
test('network errors preserve session; rejected refresh clears it', async () => {
 const storage = store({ ...session, expires_at: 1 })
 const offline = createAuthClient({ storage, fetchImpl: async () => { throw new TypeError('offline') } })
 await assert.rejects(() => offline.restore())
 assert.ok(await storage.read())
 const rejected = createAuthClient({ storage, fetchImpl: async () => json({ error: { message: 'invalid' } }, 401) })
 await assert.rejects(() => rejected.restore())
 assert.equal(await storage.read(), null)
})
test('unauthorized account refreshes at most once', async () => {
 let refreshes = 0
 const storage = store(session)
 const client = createAuthClient({ storage, now: () => 1000000, fetchImpl: async url => {
  if (url.endsWith('/refresh')) { refreshes++; return json({ session: fresh }) }
  return json({ error: { message: 'invalid' } }, 401)
 } })
 await assert.rejects(() => client.restore())
 assert.equal(refreshes, 1)
 assert.equal(await storage.read(), null)
})
test('logout clears local storage even if backend is offline', async () => {
 const storage = store(session)
 const client = createAuthClient({ storage, fetchImpl: async () => { throw new TypeError('offline') } })
 await assert.rejects(() => client.logout())
 assert.equal(await storage.read(), null)
})
test('registration confirmation does not create a session', async () => {
 const storage = store()
 const client = createAuthClient({ storage, fetchImpl: async () => json({ message: 'Periksa email' }, 202) })
 assert.equal((await client.register({})).message, 'Periksa email')
 assert.equal(await storage.read(), null)
})

test('session storage restricts access and persists across client instances', async () => {
 const { createSessionStorage } = await import('./auth.js')
 const values = {}
 let accessLevel
 const chromeApi = { storage: { session: {
  setAccessLevel: async options => { accessLevel = options.accessLevel },
  get: async key => ({ [key]: values[key] }),
  set: async data => { Object.assign(values, data) },
  remove: async key => { delete values[key] },
 } } }
 const first = createSessionStorage(chromeApi)
 await first.write(session)
 const second = createSessionStorage(chromeApi)
 assert.deepEqual(await second.read(), session)
 assert.equal(accessLevel, 'TRUSTED_CONTEXTS')
 await second.clear()
 assert.equal(await first.read(), null)
})
