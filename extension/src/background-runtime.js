import { createAudioCapture } from './audio-capture.js'
import { createAuthClient, createSessionStorage } from './auth.js'
import { createMeetingTracker } from './meetings.js'

export function startBackground(chromeApi, { baseUrl = '', fetchImpl = globalThis.fetch, auth: suppliedAuth } = {}) {
  const { local, session } = chromeApi.storage
  const authStorage = createSessionStorage(chromeApi)
  const auth = suppliedAuth ?? createAuthClient({ storage: authStorage, baseUrl, fetchImpl })
  let authQueue = Promise.resolve(), scanVersion = 0
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
  const safely = promise => { promise.catch(() => {}) }
  async function scan() {
    const version = ++scanVersion
    tracker.invalidate()
    const window = await chromeApi.windows.getLastFocused({ windowTypes: ['normal'] })
    const tabs = window?.focused ? await chromeApi.tabs.query({ active: true, windowId: window.id }) : []
    if (version !== scanVersion) return null
    return tracker.inspect(tabs[0] ?? null)
  }
  async function retry() { await tracker.retry(); return scan() }
  async function dispatch(message) {
    if (message.type === 'meeting.inspect') return scan()
    if (message.type === 'meeting.retry') { await tracker.retry({ force: true }); return scan() }
    if (message.type === 'audio.start') {
      // The worker owns the meeting choice; the popup cannot point a capture at another meeting.
      const state = await scan()
      if (state?.status !== 'registered') throw new Error('Belum ada meeting aktif yang terdaftar.')
      return capture.start({ meetingId: state.meeting_id, tabId: state.tab_id })
    }
    if (message.type === 'audio.stop') return capture.stop()
    if (message.type === 'audio.status') return capture.read()
    if (message.type === 'audio.progress') return capture.progress(message.input)
    if (message.type === 'audio.error') return capture.fail(message.input?.message ?? 'Capture gagal.', { source: message.input?.source })
    if (message.type === 'audio.token') {
      const auth = await credentials()
      return { token: auth?.accessToken ?? null }
    }
    const operation = message.type.slice('auth.'.length)
    if (['login', 'register', 'logout'].includes(operation)) {
      ++scanVersion
      tracker.invalidate()
      if (operation === 'logout') await capture.stop()
      if (operation === 'logout') await tracker.clear()
      await session.remove('meeting_account')
    }
    try {
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
      if (userId) await tracker.reconcileAccount(userId)
      return result
    } finally {
      // Token removal must precede the final clear: tab events can race the first clear.
      if (operation === 'logout') await tracker.clear()
      safely(retry())
    }
  }
  // Register synchronously before initialization promises: MV3 workers may be restarted per event.
  const popupTypes = ['auth.restore', 'auth.login', 'auth.register', 'auth.logout', 'meeting.inspect', 'meeting.retry', 'audio.start', 'audio.stop', 'audio.status']
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
  chromeApi.tabs.onUpdated.addListener((_tabId, changes) => {
    if (changes.url !== undefined || changes.status) safely(scan())
  })
  chromeApi.tabs.onRemoved.addListener(tabId => { safely(capture.stopIfTab(tabId)); safely(scan()) })
  chromeApi.windows.onFocusChanged.addListener(() => { safely(scan()) })
  chromeApi.runtime.onStartup.addListener(() => { safely(retry()) })
  chromeApi.runtime.onInstalled.addListener(() => { safely(retry()) })
  chromeApi.alarms.onAlarm.addListener(alarm => { if (alarm.name === 'meeting-sync') safely(retry()) })
  chromeApi.storage.onChanged.addListener((changes, area) => {
    if (area !== 'session' || !changes.auth_session) return
    ++scanVersion
    tracker.invalidate()
    if (!changes.auth_session.newValue) {
      safely(session.remove('meeting_account'))
      safely(scan())
    } else safely(scan())
  })
  // Restrict local URL metadata to extension contexts, like authentication session storage.
  safely(Promise.resolve(local.setAccessLevel({ accessLevel: 'TRUSTED_CONTEXTS' })))
  safely(Promise.resolve(chromeApi.alarms.create('meeting-sync', { periodInMinutes: 1 })))
  return { scan, retry }
}
