import { createAudioCapture } from './audio-capture.js'
import { createAuthClient, createSessionStorage } from './auth.js'
import { createMeetingTracker } from './meetings.js'
import { createTranscriptClient } from './saved-transcripts.js'

export function startBackground(chromeApi, { baseUrl = '', fetchImpl = globalThis.fetch, auth: suppliedAuth } = {}) {
  const { local, session } = chromeApi.storage
  const authStorage = createSessionStorage(chromeApi)
  const auth = suppliedAuth ?? createAuthClient({ storage: authStorage, baseUrl, fetchImpl })
  let authQueue = Promise.resolve(), scanVersion = 0
  let authGeneration = 0, authChanging = false
  let authLifecycle = Promise.resolve()
  let archiveTicket = 0
  const runAuth = operation => {
    const result = authQueue.then(operation)
    authQueue = result.catch(() => {})
    return result
  }
  async function remember(account) {
    if (account?.user?.id) await session.set({ meeting_account: { id: account.user.id } })
    return account
  }
  const credentials = ({ force = false } = {}) => runAuth(async () => {
    let tokens = await authStorage.read()
    if (!tokens) return null
    let account = (await session.get('meeting_account')).meeting_account
    if (force || !account?.id || tokens.expires_at <= Date.now() / 1000 + 60) {
      try {
        const restored = await remember(await auth.restore())
        if (!restored) return null
        account = { id: restored.user.id }
        tokens = await authStorage.read()
      } catch (error) {
        // Preserve a known account and let resolve remain pending while offline.
        if (error.status === 401 || !account?.id || !await authStorage.read()) throw error
      }
    }
    return tokens?.access_token ? { userId: account.id, accessToken: tokens.access_token } : null
  })
  const tracker = createMeetingTracker({ local, session, credentials, fetchImpl, baseUrl })
  const capture = createAudioCapture({ chromeApi, session, credentials, fetchImpl, baseUrl })
  const transcripts = createTranscriptClient({ credentials, fetchImpl, baseUrl, context: async () => {
    const generation = authGeneration
    const account = (await session.get('meeting_account')).meeting_account
    const meeting = (await session.get('meeting_status')).meeting_status
    const tokens = await authStorage.read()
    return { ownerId: authChanging || !tokens || generation !== authGeneration ? null : account?.id, meetingId: meeting?.status === 'registered' && meeting.user_id === account?.id ? meeting.meeting_id : null, generation }
  } })
  async function reconcileCapture(userId) {
    const state = await capture.read()
    const transcript = (await session.get('transcript_state')).transcript_state
    if (!userId || (state && state.owner_id !== userId) || (transcript && transcript.owner_id !== userId)) {
      capture.invalidate()
      await capture.stop()
      await capture.clear()
    }
  }
  const safely = promise => { promise.catch(() => {}) }
  async function finishOccurrence(stopped) {
    if (stopped?.stop_acknowledged === false) return stopped
    const occurrence = (await session.get('meeting_occurrence')).meeting_occurrence
    if (!occurrence?.id || occurrence.status !== 'active') return stopped
    const identity = await credentials()
    if (!identity) throw new Error('Masuk kembali untuk mengakhiri meeting.')
    const response = await fetchImpl(`${baseUrl}/api/occurrences/${occurrence.id}/finish`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${identity.accessToken}`, 'Content-Type': 'application/json' },
      body: '{}',
    })
    const data = await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(data?.error?.message || 'Meeting belum dapat diakhiri.')
    if (data?.occurrence?.id !== occurrence.id || data.occurrence.status !== 'awaiting_decision') throw new Error('Respons akhir meeting tidak valid.')
    await session.set({ meeting_occurrence: data.occurrence })
    return { ...(stopped ?? {}), occurrence: data.occurrence }
  }
  async function stopTabOccurrence(tabId) {
    const state = await capture.read()
    if (state?.tab_id !== tabId || !state.run_id) return
    await capture.stopIfTab(tabId)
    return finishOccurrence()
  }
  async function scan() {
    const version = ++scanVersion
    tracker.invalidate()
    const window = await chromeApi.windows.getLastFocused({ windowTypes: ['normal'] })
    const tabs = window?.focused ? await chromeApi.tabs.query({ active: true, windowId: window.id }) : []
    if (version !== scanVersion) return null
    const state = await tracker.inspect(tabs[0] ?? null)
    const dismissed = (await session.get('connection_offer_dismissed')).connection_offer_dismissed
    const sameOffer = state?.status === 'registered' && dismissed?.tab_id === state.tab_id && dismissed?.canonical_url === state.canonical_url
    if (state?.status !== 'registered' && dismissed) await session.remove('connection_offer_dismissed')
    return sameOffer ? { ...state, offer_dismissed: true } : state
  }
  async function retry() { await tracker.retry(); return scan() }
  function dispatch(message) {
    if (!message.type.startsWith('auth.')) return dispatchOperation(message)
    const result = authLifecycle.then(() => dispatchOperation(message))
    authLifecycle = result.catch(() => {})
    return result
  }
  async function dispatchOperation(message) {
    if (authChanging && !message.type.startsWith('audio.') && !message.type.startsWith('auth.')) throw new Error('Sesi sedang berubah.')
    if (message.type === 'transcript.runs.list') return transcripts.list(message.input)
    if (message.type === 'transcript.run.read' || message.type === 'transcript.run.delete') {
      const ticket = ++archiveTicket
      const data = await transcripts[message.type === 'transcript.run.read' ? 'read' : 'delete'](message.input)
      if (ticket === archiveTicket) safely(capture.storage(data.run, data.storage))
      return data
    }
    if (message.type === 'meeting.inspect') return scan()
    if (message.type === 'meeting.retry') { await tracker.retry({ force: true }); return scan() }
    if (message.type === 'translation.current.read') {
      const state = await capture.read()
      const identity = await credentials()
      if (!state?.run_id || !identity || state.owner_id !== identity.userId) return null
      const response = await fetchImpl(`${baseUrl}/api/audio/runs/${state.run_id}/translations`, {
        headers: { Authorization: `Bearer ${identity.accessToken}` },
      })
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data?.error?.message || 'Terjemahan belum dapat dimuat.')
      const next = { owner_id: identity.userId, run_id: state.run_id, translations: data.translations ?? [] }
      await session.set({ translation_state: next })
      return next
    }
    if (message.type === 'meeting.offer.dismiss') {
      const state = await scan()
      if (state?.status !== 'registered') return state
      const dismissed = { tab_id: state.tab_id, canonical_url: state.canonical_url }
      await session.set({ connection_offer_dismissed: dismissed })
      return { ...state, offer_dismissed: true }
    }
    if (message.type === 'archives.list') {
      const identity = await credentials()
      if (!identity) return { archives: [], next_cursor: null }
      const cursor = message.input?.cursor
      if (cursor !== undefined && (typeof cursor !== 'string' || cursor.length > 2048)) throw new Error('Paginasi arsip tidak valid.')
      const query = new URLSearchParams({ limit: '20' })
      if (cursor) query.set('cursor', cursor)
      const response = await fetchImpl(`${baseUrl}/api/archives?${query}`, { headers: { Authorization: `Bearer ${identity.accessToken}` } })
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data?.error?.message || 'Riwayat arsip gagal dimuat.')
      return data
    }
    if (message.type === 'archive.detail') {
      const identity = await credentials(), id = message.input?.id
      if (!identity || typeof id !== 'string') throw new Error('Arsip tidak valid.')
      const response = await fetchImpl(`${baseUrl}/api/archives/${id}`, { headers: { Authorization: `Bearer ${identity.accessToken}` } })
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data?.error?.message || 'Detail arsip gagal dimuat.')
      return data
    }
    if (message.type === 'archive.delete' || message.type === 'archive.export') {
      const identity = await credentials(), id = message.input?.id
      if (!identity || typeof id !== 'string') throw new Error('Arsip tidak valid.')
      if (message.type === 'archive.delete') {
        const response = await fetchImpl(`${baseUrl}/api/archives/${id}`, { method: 'DELETE', headers: { Authorization: `Bearer ${identity.accessToken}` } })
        if (!response.ok) { const data = await response.json().catch(() => ({})); throw new Error(data?.error?.message || 'Arsip gagal dihapus.') }
        const current = (await session.get('meeting_occurrence')).meeting_occurrence
        if (current?.status === 'archived') await session.remove('meeting_occurrence')
        return { deleted: true }
      }
      const response = await fetchImpl(`${baseUrl}/api/archives/${id}/export`, { headers: { Authorization: `Bearer ${identity.accessToken}` } })
      if (!response.ok) { const data = await response.json().catch(() => ({})); throw new Error(data?.error?.message || 'Ekspor arsip gagal.') }
      return { bytes: await response.arrayBuffer(), type: response.headers.get('Content-Type') ?? 'application/zip', name: `livetranslate-${id}.zip` }
    }
    if (message.type === 'questions.current.read') {
      const occurrence = (await session.get('meeting_occurrence')).meeting_occurrence
      const identity = await credentials()
      if (!occurrence?.id || !identity) return { questions: [] }
      const response = await fetchImpl(`${baseUrl}/api/questions?occurrence_id=${encodeURIComponent(occurrence.id)}`, { headers: { Authorization: `Bearer ${identity.accessToken}` } })
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data?.error?.message || 'Pertanyaan belum dapat dimuat.')
      return data
    }
    if (message.type === 'document.upload') {
      const occurrence = (await session.get('meeting_occurrence')).meeting_occurrence
      const identity = await credentials()
      const input = message.input ?? {}
      const allowed = new Set(['application/pdf', 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'text/csv'])
      if (!occurrence?.id || !['pending_documents', 'active'].includes(occurrence.status)) throw new Error('Hubungkan meeting sebelum mengunggah dokumen.')
      if (!identity || !(input.bytes instanceof ArrayBuffer) || input.bytes.byteLength < 1 || input.bytes.byteLength > 20 * 1024 * 1024 || !allowed.has(input.type) || typeof input.name !== 'string') throw new Error('Dokumen tidak valid.')
      const form = new FormData()
      form.set('occurrence_id', occurrence.id)
      form.set('file', new Blob([input.bytes], { type: input.type }), input.name)
      const response = await fetchImpl(`${baseUrl}/api/documents`, { method: 'POST', headers: { Authorization: `Bearer ${identity.accessToken}` }, body: form })
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data?.error?.message || 'Upload dokumen gagal.')
      return data
    }
    if (message.type === 'documents.list') {
      const occurrence = (await session.get('meeting_occurrence')).meeting_occurrence
      const identity = await credentials()
      if (!occurrence?.id || !identity) return { documents: [] }
      const response = await fetchImpl(`${baseUrl}/api/documents?occurrence_id=${encodeURIComponent(occurrence.id)}`, { headers: { Authorization: `Bearer ${identity.accessToken}` } })
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data?.error?.message || 'Daftar dokumen gagal dimuat.')
      return data
    }
    if (message.type === 'occurrence.connect') {
      const state = await scan()
      if (state?.status !== 'registered') throw new Error('Belum ada meeting aktif yang terdaftar.')
      const identity = await credentials()
      if (!identity) throw new Error('Masuk kembali untuk terhubung ke meeting.')
      const response = await fetchImpl(`${baseUrl}/api/occurrences/connect`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${identity.accessToken}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ meeting_id: state.meeting_id }),
      })
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data?.error?.message || 'Tidak dapat terhubung ke meeting.')
      if (!data?.occurrence?.id || data.occurrence.meeting_id !== state.meeting_id || data.occurrence.status !== 'pending_documents') throw new Error('Respons occurrence tidak valid.')
      await session.set({ meeting_occurrence: data.occurrence })
      await session.remove('connection_offer_dismissed')
      return data
    }
    if (message.type === 'occurrence.archive' || message.type === 'occurrence.discard') {
      const occurrence = (await session.get('meeting_occurrence')).meeting_occurrence
      if (!occurrence?.id || occurrence.status !== 'awaiting_decision') throw new Error('Tidak ada keputusan meeting yang menunggu.')
      const identity = await credentials()
      if (!identity) throw new Error('Masuk kembali untuk menyimpan keputusan meeting.')
      const action = message.type.endsWith('archive') ? 'archive' : 'discard'
      const response = await fetchImpl(`${baseUrl}/api/occurrences/${occurrence.id}/${action}`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${identity.accessToken}`, 'Content-Type': 'application/json' },
        body: '{}',
      })
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data?.error?.message || 'Keputusan penyimpanan gagal.')
      const next = action === 'archive'
        ? { ...occurrence, status: 'archived', staging_expires_at: null }
        : data?.occurrence
      if (next?.id !== occurrence.id || !['archived', 'discarded'].includes(next.status)) throw new Error('Respons keputusan meeting tidak valid.')
      await session.set({ meeting_occurrence: next })
      return { ...data, occurrence: next }
    }
    if (message.type === 'audio.start') {
      if (authChanging) throw new Error('Sesi sedang berubah.')
      const input = message.input ?? {}
      if (Object.keys(input).length !== 0) throw new Error('Mulai capture tidak menerima pilihan arsip.')
      // The worker owns both room and occurrence identity; popup input cannot
      // redirect capture to another user's meeting.
      const state = await scan()
      if (authChanging) throw new Error('Sesi sedang berubah.')
      if (state?.status !== 'registered') throw new Error('Belum ada meeting aktif yang terdaftar.')
      const identity = await credentials()
      if (!identity) throw new Error('Masuk kembali untuk mulai capture.')
      const request = async (path, body) => {
        const response = await fetchImpl(baseUrl + path, {
          method: 'POST',
          headers: { Authorization: `Bearer ${identity.accessToken}`, 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        })
        const data = await response.json().catch(() => ({}))
        if (!response.ok) throw new Error(data?.error?.message || 'Lifecycle meeting tidak dapat diperbarui.')
        return data
      }
      const occurrence = (await session.get('meeting_occurrence')).meeting_occurrence
      if (!occurrence?.id || occurrence.meeting_id !== state.meeting_id || !['pending_documents', 'active'].includes(occurrence.status)) {
        throw new Error('Hubungkan LiveTranslate ke meeting sebelum mulai merekam.')
      }
      const occurrenceId = occurrence.id
      const activated = occurrence.status === 'active' ? { occurrence } : await request(`/api/occurrences/${occurrenceId}/activate`, {})
      if (activated?.occurrence?.id !== occurrenceId || activated.occurrence.status !== 'active') throw new Error('Occurrence meeting tidak aktif.')
      await session.set({ meeting_occurrence: activated.occurrence })
      return capture.start({ meetingId: state.meeting_id, occurrenceId, tabId: state.tab_id })
    }
    if (message.type === 'audio.stop') return finishOccurrence(await capture.stop())
    if (message.type === 'audio.status') {
      const state = await capture.read(), account = (await session.get('meeting_account')).meeting_account
      return !authChanging && state?.owner_id === account?.id ? state : null
    }
    if (message.type === 'audio.progress') return capture.progress(message.input)
    if (message.type === 'audio.error') {
      const failed = await capture.fail(message.input?.message ?? 'Capture gagal.', message.input)
      return ['transcript', 'terminal'].includes(message.input?.source) ? finishOccurrence(failed) : failed
    }
    if (message.type === 'audio.token') return { token: await capture.token(message.input) }
    const operation = message.type.slice('auth.'.length)
    let ownerTransitionReady = false
    try {
      if (['login', 'register', 'logout'].includes(operation)) {
        authChanging = true
        authGeneration += 1
        capture.invalidate()
        ++scanVersion
        tracker.invalidate()
        // Drain/stop and finish the old owner's occurrence while its token still exists.
        const stopped = await capture.stop()
        await finishOccurrence(stopped)
        ownerTransitionReady = true
        await capture.clear()
        if (operation === 'logout') await tracker.clear()
        await session.remove('meeting_occurrence')
        await session.remove('meeting_account')
      }
      const result = await runAuth(async () => {
        if (operation === 'restore') return remember(await auth.restore())
        if (operation === 'login') return remember(await auth.login(message.input))
        if (operation === 'register') {
          const result = await auth.register(message.input)
          if (result.account) await remember(result.account)
          return result
        }
        return auth.logout()
      })
      const userId = result?.user?.id ?? result?.account?.user?.id
      await reconcileCapture(userId ?? null)
      if (userId) await tracker.reconcileAccount(userId)
      return result
    } finally {
      authChanging = false
      if (operation === 'logout' && ownerTransitionReady) {
        await tracker.clear()
        await capture.clear()
      }
      safely(retry())
    }
  }
  // Register synchronously before initialization promises: MV3 workers may be restarted per event.
  const popupTypes = ['auth.restore', 'auth.login', 'auth.register', 'auth.logout', 'meeting.inspect', 'meeting.retry', 'meeting.offer.dismiss', 'occurrence.connect', 'occurrence.archive', 'occurrence.discard', 'document.upload', 'documents.list', 'questions.current.read', 'archives.list', 'archive.detail', 'archive.delete', 'archive.export', 'audio.start', 'audio.stop', 'audio.status', 'translation.current.read', 'transcript.runs.list', 'transcript.run.read', 'transcript.run.delete']
  const offscreenTypes = ['audio.progress', 'audio.error', 'audio.token']
  chromeApi.runtime.onMessage.addListener((message, sender, respond) => {
    if (sender.id !== chromeApi.runtime.id) return false
    const type = message?.type
    if (offscreenTypes.includes(type)) {
      if (!sender.url?.endsWith('offscreen.html')) return false
      dispatch(message).then(data => respond({ ok: true, data })).catch(error => respond({ ok: false, error: { message: error.message || 'Operasi gagal.', status: error.status ?? 0 } }))
      return true
    }
    if (sender.tab || !popupTypes.includes(type)) return false
    dispatch(message).then(data => respond({ ok: true, data })).catch(error => respond({ ok: false, error: { message: error.message || 'Operasi gagal.', status: error.status ?? 0 } }))
    return true
  })
  chromeApi.tabs.onActivated.addListener(() => { safely(scan()) })
  chromeApi.tabs.onUpdated.addListener((tabId, changes) => {
    if (changes.url !== undefined) safely(stopTabOccurrence(tabId))
    if (changes.url !== undefined || changes.status) safely(scan())
  })
  chromeApi.tabs.onRemoved.addListener(tabId => { safely(stopTabOccurrence(tabId)); safely(scan()) })
  chromeApi.windows.onFocusChanged.addListener(() => { safely(scan()) })
  chromeApi.runtime.onStartup.addListener(() => { safely(retry()) })
  chromeApi.runtime.onInstalled.addListener(() => { safely(retry()) })
  chromeApi.alarms.onAlarm.addListener(alarm => { if (alarm.name === 'meeting-sync') safely(retry()) })
  chromeApi.storage.onChanged.addListener((changes, area) => {
    if (area !== 'session') return
    if (changes.meeting_status) {
      const { oldValue: previous, newValue: next } = changes.meeting_status
      if (previous?.tab_id !== next?.tab_id || previous?.canonical_url !== next?.canonical_url || (previous?.meeting_id && next?.meeting_id && previous.meeting_id !== next.meeting_id)) authGeneration += 1
    }
    if (changes.meeting_account) {
      const previous = changes.meeting_account.oldValue?.id, next = changes.meeting_account.newValue?.id
      if (previous !== next) {
        authGeneration += 1
        if (!authChanging) safely(reconcileCapture(next ?? null))
      }
    }
    if (!changes.auth_session) return
    ++scanVersion
    tracker.invalidate()
    if (!changes.auth_session.newValue) {
      authGeneration += 1
      capture.invalidate()
      if (!authChanging) safely(capture.stop().finally(() => capture.clear()))
      safely(session.remove('meeting_account'))
      safely(scan())
    } else safely(scan())
  })
  // Restrict local URL metadata to extension contexts, like authentication session storage.
  safely(Promise.resolve(local.setAccessLevel({ accessLevel: 'TRUSTED_CONTEXTS' })))
  safely(Promise.resolve(chromeApi.alarms.create('meeting-sync', { periodInMinutes: 1 })))
  return { scan, retry }
}
