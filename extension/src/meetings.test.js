import test from 'node:test'
import assert from 'node:assert/strict'
import * as meetings from './meetings.js'
const room = 'https://meet.google.com/abc-defg-hij'
const row = { id: 'aaaaaaaa-2222-4333-8444-555555555555', user_id: 'user1', provider: 'google_meet', provider_host: 'meet.google.com', provider_room_id: 'abc-defg-hij', meeting_key: 'google_meet:meet.google.com:abc-defg-hij', canonical_url: room, created_at: '2026-10-18T00:00:00Z' }
function area() {
 const values = {}
 return { values, async get(key) { return { [key]: structuredClone(values[key]) } }, async set(value) { Object.assign(values, structuredClone(value)) }, async remove(key) { delete values[key] } }
}
function setup(options = {}) {
 const local = options.local ?? area(), session = area()
 let user = 'user1', count = 0, offline = false
 const tracker = meetings.createMeetingTracker?.({ local, session, credentials: async () => user ? { userId: user, accessToken: 'access' } : null,
 fetchImpl: async (url, init) => {
  count++
  assert.equal(url, 'https://backend.test/api/meetings/resolve')
  assert.equal(init.headers.Authorization, 'Bearer access')
  assert.deepEqual(JSON.parse(init.body), { canonical_url: room })
  if (offline) throw new Error('offline')
  return Response.json({ meeting: { ...row, user_id: user } })
 }, baseUrl: 'https://backend.test', ...options })
 return { tracker, local, session, count: () => count, setUser: value => { user = value }, offline: value => { offline = value } }
}
test('refresh, second tab and worker restart reuse persisted meeting identity', async () => {
 const f = setup()
 assert.ok(f.tracker, 'meeting tracker exists')
 assert.equal((await f.tracker.inspect({ id: 1, url: room + '?authuser=1' })).meeting_id, row.id)
 assert.equal((await f.tracker.inspect({ id: 1, url: room })).meeting_id, row.id)
 assert.equal((await f.tracker.inspect({ id: 2, url: room })).meeting_id, row.id)
 assert.equal(f.count(), 1)
 const restarted = setup({ local: f.local })
 assert.equal((await restarted.tracker.inspect({ id: 3, url: room })).meeting_id, row.id)
 assert.equal(restarted.count(), 0)
 await f.tracker.inspect({ id: 1, url: 'https://example.com/' })
 assert.equal((await f.tracker.inspect({ id: 1, url: room })).meeting_id, row.id)
 assert.ok(!JSON.stringify(f.local.values).includes('authuser'))
 assert.ok(!JSON.stringify(f.local.values).includes('access'))
})
test('offline detection persists pending canonical URL and retries without popup', async () => {
 const f = setup(); assert.ok(f.tracker)
 f.offline(true)
 assert.equal((await f.tracker.inspect({ id: 1, url: room + '?pwd=SECRET' })).status, 'sync_error')
 assert.ok(!JSON.stringify(f.local.values).includes('SECRET'))
 const restarted = setup({ local: f.local })
 await restarted.tracker.retry({ force: true })
 assert.equal(restarted.count(), 1)
 assert.equal((await restarted.tracker.inspect({ id: 1, url: room })).meeting_id, row.id)
 assert.equal(restarted.count(), 1)
})
test('logout invalidates an in-flight result and clears persisted account state', async () => {
 let finish, started
 const ready = new Promise(resolve => { started = resolve })
 const f = setup({ fetchImpl: async () => { started(); return new Promise(resolve => { finish = () => resolve(Response.json({ meeting: row })) }) } })
 assert.ok(f.tracker)
 const pending = f.tracker.inspect({ id: 1, url: room }); await ready
 f.tracker.invalidate()
 const cleared = f.tracker.clear()
 f.setUser(null); finish(); await pending; await cleared
 assert.equal(f.local.values.meeting_store, undefined)
 assert.equal((await f.tracker.inspect({ id: 1, url: room })).status, 'login_required')
})
test('a different account never reuses previous account cache', async () => {
 const f = setup(); assert.ok(f.tracker)
 await f.tracker.inspect({ id: 1, url: room })
 f.setUser('user2'); f.tracker.invalidate()
 assert.equal((await f.tracker.inspect({ id: 1, url: room })).user_id, 'user2')
 assert.equal(f.count(), 2)
})
test('late response does not replace status of a newly selected tab', async () => {
 let finish, started
 const ready = new Promise(resolve => { started = resolve })
 const f = setup({ fetchImpl: async () => { started(); return new Promise(resolve => { finish = () => resolve(Response.json({ meeting: row })) }) } })
 assert.ok(f.tracker)
 const first = f.tracker.inspect({ id: 1, url: room }); await ready
 const next = f.tracker.inspect({ id: 2, url: 'https://example.com/' })
 finish(); await first; await next
 assert.equal(f.session.values.meeting_status.status, 'unsupported')
 assert.equal(f.session.values.meeting_status.tab_id, 2)
})
test('resolve retries unauthorized access with refreshed credentials for the same account', async () => {
 let refreshes = 0, calls = 0
 const f = setup({ credentials: async options => { if (options?.force) refreshes++; return { userId: 'user1', accessToken: options?.force ? 'new' : 'old' } }, fetchImpl: async (_url, init) => {
  calls++
  if (init.headers.Authorization === 'Bearer old') return Response.json({}, { status: 401 })
  return Response.json({ meeting: row })
 } })
 assert.equal((await f.tracker.inspect({ id: 1, url: room })).status, 'registered')
 assert.equal(refreshes, 1)
 assert.equal(calls, 2)
})
test('invalidated auth preserves pending work until the same user signs in again', async () => {
 const f = setup(); f.offline(true)
 await f.tracker.inspect({ id: 1, url: room })
 f.setUser(null); f.tracker.invalidate()
 await f.tracker.retry({ force: true })
 assert.ok(f.local.values.meeting_store.pending[row.meeting_key])
 f.setUser('user1'); f.offline(false)
 await f.tracker.reconcileAccount('user1')
 await f.tracker.retry({ force: true })
 assert.equal(f.local.values.meeting_store.meetings[row.meeting_key].id, row.id)
})
