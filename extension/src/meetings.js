import { classifyMeetingURL } from './meeting-url.js'

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
function validMeeting(value, identity, userId) {
  return value && uuid.test(value.id) && value.user_id === userId && value.meeting_key === identity.meeting_key
    && value.canonical_url === identity.canonical_url && value.provider === identity.provider
    && value.provider_host === identity.provider_host && value.provider_room_id === identity.provider_room_id
}

// One writer owns the persistent cache/outbox. Never persist credentials or raw tab URLs.
export function createMeetingTracker({ local, session, credentials, fetchImpl = globalThis.fetch, baseUrl = '', now = Date.now }) {
  let queue = Promise.resolve(), generation = 0
  const enqueue = operation => {
    const result = queue.then(operation)
    queue = result.catch(() => {})
    return result
  }
  async function publish(value, ticket) {
    if (ticket !== generation) return null
    await session.set({ meeting_status: value })
    return value
  }
  async function storeFor(userId) {
    const stored = (await local.get('meeting_store')).meeting_store
    return stored?.user_id === userId && stored.meetings && stored.pending
      ? stored : { user_id: userId, meetings: {}, pending: {} }
  }
  async function resolve(identity, auth, store, ticket, force = false) {
    const key = identity.meeting_key, cached = store.meetings[key]
    if (validMeeting(cached, identity, auth.userId)) return cached
    const previous = store.pending[key]
    if (!force && previous?.next_at > now()) throw new Error('retry_later')
    const pending = { canonical_url: identity.canonical_url, attempts: previous?.attempts ?? 0, next_at: 0 }
    store.pending[key] = pending
    if (ticket !== generation) return null
    await local.set({ meeting_store: store })
    try {
      const request = accessToken => fetchImpl(`${baseUrl.replace(/\/$/, '')}/api/meetings/resolve`, {
        method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${accessToken}` },
        body: JSON.stringify({ canonical_url: identity.canonical_url }), cache: 'no-store', signal: AbortSignal.timeout(10000),
      })
      let response = await request(auth.accessToken)
      if (response.status === 401) {
        const refreshed = await credentials({ force: true })
        if (ticket !== generation) return null
        if (refreshed?.userId === auth.userId) response = await request(refreshed.accessToken)
      }
      if (!response.ok) { const error = new Error('resolve_failed'); error.status = response.status; throw error }
      const { meeting } = await response.json()
      if (!validMeeting(meeting, identity, auth.userId)) throw new Error('invalid_meeting')
      if (ticket !== generation) return null
      // Whitelist fields; do not persist arbitrary response metadata.
      const saved = { ...identity, id: meeting.id, user_id: meeting.user_id, created_at: meeting.created_at }
      store.meetings[key] = saved
      delete store.pending[key]
      await local.set({ meeting_store: store })
      return saved
    } catch (error) {
      if (ticket === generation) {
        pending.attempts++
        pending.next_at = now() + Math.min(30 * 60000, 60000 * 2 ** Math.min(pending.attempts - 1, 5))
        await local.set({ meeting_store: store })
      }
      throw error
    }
  }
  return {
    invalidate() { generation++ },
    clear() {
      generation++
      return enqueue(async () => { await local.remove('meeting_store'); await session.remove('meeting_status') })
    },
    reconcileAccount(userId) {
      generation++
      return enqueue(async () => {
        const stored = (await local.get('meeting_store')).meeting_store
        if (stored && stored.user_id !== userId) await local.remove('meeting_store')
        await session.remove('meeting_status')
      })
    },
    inspect(tab) {
      const ticket = ++generation
      return enqueue(async () => {
        if (ticket !== generation) return null
        const identity = classifyMeetingURL(tab?.url)
        const state = { tab_id: tab?.id ?? null, status: tab ? 'unsupported' : 'no_active_tab' }
        if (!identity) return publish(state, ticket)
        Object.assign(state, { ...identity, status: 'syncing' })
        await publish(state, ticket)
        try {
          const auth = await credentials()
          if (ticket !== generation) return null
          if (!auth) { state.status = 'login_required'; return publish(state, ticket) }
          state.user_id = auth.userId
          const store = await storeFor(auth.userId)
          const meeting = await resolve(identity, auth, store, ticket)
          if (!meeting) return null
          Object.assign(state, { status: 'registered', meeting_id: meeting.id })
        } catch (error) {
          state.status = error.status === 401 ? 'login_required' : 'sync_error'
        }
        return publish(state, ticket)
      })
    },
    retry({ force = false } = {}) {
      const ticket = generation
      return enqueue(async () => {
        const auth = await credentials()
        if (!auth || ticket !== generation) return
        const store = await storeFor(auth.userId)
        // Bound each worker wake to avoid a long-running background loop.
        const pending = Object.values(store.pending).filter(item => force || item.next_at <= now()).slice(0, 10)
        for (const item of pending) {
          if (ticket !== generation) return
          const identity = classifyMeetingURL(item.canonical_url)
          if (!identity) continue
          try { await resolve(identity, auth, store, ticket, force) } catch { /* durable outbox retains retry state */ }
        }
      })
    },
  }
}
