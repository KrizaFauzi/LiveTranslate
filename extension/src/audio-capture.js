const AUDIO_KEY = 'audio_status'
const OFFSCREEN_URL = 'offscreen.html'

// createAudioCapture owns one capture run at a time: the worker decides the meeting, the popup only asks to start.
export function createAudioCapture({ chromeApi, session, credentials, fetchImpl = globalThis.fetch, baseUrl = '' }) {
  let active = null

  async function publish(value) {
    await session.set({ [AUDIO_KEY]: value })
    return value
  }
  async function read() {
    return (await session.get(AUDIO_KEY))[AUDIO_KEY] ?? null
  }
  async function api(path, token, body) {
    const response = await fetchImpl(`${baseUrl.replace(/\/$/, '')}${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
      body: JSON.stringify(body ?? {}),
      cache: 'no-store',
      signal: AbortSignal.timeout(15000),
    })
    const data = await response.json().catch(() => null)
    if (!response.ok) {
      const error = new Error(data?.error?.message || 'Permintaan gagal.')
      error.status = response.status
      throw error
    }
    return data
  }
  async function ensureOffscreen() {
    const offscreen = chromeApi.offscreen
    if (!offscreen) throw new Error('Browser ini tidak mendukung dokumen offscreen.')
    if (!(await offscreen.hasDocument())) {
      await offscreen.createDocument({
        url: OFFSCREEN_URL,
        reasons: ['USER_MEDIA'],
        justification: 'Menangkap audio tab meeting untuk transkripsi.',
      })
    }
  }
  async function toOffscreen(type, input) {
    let response
    try {
      response = await chromeApi.runtime.sendMessage({ type, input })
    } catch {
      throw new Error('Dokumen capture audio tidak dapat dihubungi.')
    }
    if (response?.ok === false) throw new Error(response?.error?.message || 'Dokumen capture audio gagal menjalankan permintaan.')
    return response
  }

  async function stop() {
    const stored = await read()
    const current = active ?? stored
    if (!current?.run_id) return stored
    // The offscreen document may already be closed; the run is closed on the server either way.
    await toOffscreen('audio.capture.stop', { run_id: current.run_id }).catch(() => {})
    try {
      await chromeApi.offscreen?.closeDocument?.()
    } catch {
      // Closing an absent document is not an error for the caller.
    }
    const auth = await credentials()
    if (auth) await api(`/api/audio/runs/${current.run_id}/stop`, auth.accessToken).catch(() => {})
    active = null
    return publish({ ...stored, ...current, status: 'stopped', stopped_at: new Date().toISOString() })
  }

  return {
    read,
    async start({ meetingId, tabId }) {
      if (active) {
        const current = await read()
        // A capture that failed still owns a run on the server: close it before starting a new one.
        if (current?.status !== 'error') return current ?? read()
        await stop()
      }
      if (!meetingId || !tabId) throw new Error('Belum ada meeting aktif yang terdaftar.')
      const auth = await credentials()
      if (!auth) {
        const error = new Error('Masuk kembali untuk mulai capture.')
        error.status = 401
        throw error
      }
      const { run } = await api('/api/audio/runs', auth.accessToken, { meeting_id: meetingId })
      try {
        const streamId = await chromeApi.tabCapture.getMediaStreamId({ targetTabId: tabId })
        await ensureOffscreen()
        await toOffscreen('audio.capture.start', {
          run_id: run.id,
          stream_id: streamId,
          token: auth.accessToken,
          base_url: baseUrl,
        })
      } catch (error) {
        // The run was created but the capture never started: close it again on the server.
        await api(`/api/audio/runs/${run.id}/stop`, auth.accessToken).catch(() => {})
        throw error
      }
      active = { run_id: run.id, meeting_id: meetingId, tab_id: tabId }
      return publish({
        status: 'capturing',
        run_id: run.id,
        meeting_id: meetingId,
        tab_id: tabId,
        started_at: run.started_at,
        chunks: 0,
        bytes: 0,
      })
    },
    stop,
    progress(input) {
      return read().then(current => {
        // A failed or stopped capture is never resurrected by a late progress message.
        const status = current?.status === 'error' || current?.status === 'stopped' ? current.status : 'capturing'
        return publish({ ...(current ?? {}), ...input, status })
      })
    },
    async fail(message) {
      const stored = await read()
      // The run stays in the state so the stop path can still close it on the server.
      return publish({ ...stored, ...(active ?? {}), status: 'error', message, failed_at: new Date().toISOString() })
    },
    async stopIfTab(tabId) {
      const current = active ?? (await read())
      if (current?.tab_id !== tabId || !current?.run_id) return
      if (!active && current.status !== 'capturing') return
      await stop()
    },
  }
}
