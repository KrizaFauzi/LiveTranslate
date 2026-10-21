const AUDIO_KEY = 'audio_status'
const OFFSCREEN_URL = 'offscreen.html'

// createAudioCapture owns one capture run at a time: the worker decides the meeting, the popup only asks to start.
export function createAudioCapture({ chromeApi, session, credentials, fetchImpl = globalThis.fetch, baseUrl = '' }) {
  let active = null
  // Every state change is a read-modify-write cycle on the session storage, so the operations run
  // one after another: a late progress message must never overwrite a failure.
  let stateQueue = Promise.resolve()
  function serialize(operation) {
    const result = stateQueue.then(operation)
    stateQueue = result.catch(() => {})
    return result
  }

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

  // The backend needs the sample rate of the run, and only the capture knows it: the document
  // offscreen reports the rate of its audio context, which is the rate the audio is recorded in.
  async function probeSampleRate() {
    const response = await toOffscreen('audio.capture.probe', {})
    const rate = response?.sample_rate
    if (!Number.isInteger(rate) || rate <= 0) throw new Error('Sample rate audio tidak dapat dibaca.')
    return rate
  }

  async function stopRun() {
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

  async function startRun({ meetingId, tabId }) {
    const stored = await read()
    if (stored?.run_id && stored.status !== 'stopped') {
      // A previous capture still owns a run on the server: close it before starting a new one. The
      // stored state is the source of truth, the worker may have been restarted meanwhile.
      if (stored.status === 'error') await stopRun()
      else return stored
    }
    if (!meetingId || !tabId) throw new Error('Belum ada meeting aktif yang terdaftar.')
    const auth = await credentials()
    if (!auth) {
      const error = new Error('Masuk kembali untuk mulai capture.')
      error.status = 401
      throw error
    }
    // The document offscreen has to exist before it can report its sample rate.
    await ensureOffscreen()
    const sampleRate = await probeSampleRate()
    const { run } = await api('/api/audio/runs', auth.accessToken, { meeting_id: meetingId, sample_rate: sampleRate })
    try {
      const streamId = await chromeApi.tabCapture.getMediaStreamId({ targetTabId: tabId })
      await toOffscreen('audio.capture.start', {
        run_id: run.id,
        stream_id: streamId,
        token: auth.accessToken,
        base_url: baseUrl,
        sample_rate: sampleRate,
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
  }

  async function progressRun(input) {
    const current = await read()
    // A failed or stopped capture is never resurrected by a late progress message.
    const status = current?.status === 'error' || current?.status === 'stopped' ? current.status : 'capturing'
    return publish({ ...(current ?? {}), ...input, status })
  }

  async function failRun(message) {
    const stored = await read()
    // Only a run that is still open may be reported as failed: a run that already stopped leaves no
    // run id behind, so the popup never shows a stale run.
    const failed = active ?? (stored?.status === 'stopped' ? null : stored)
    const state = { ...(stored ?? {}) }
    delete state.run_id
    return publish({ ...state, ...(failed ?? {}), status: 'error', message, failed_at: new Date().toISOString() })
  }

  async function stopIfTabRun(tabId) {
    const stored = await read()
    const current = active ?? stored
    if (current?.tab_id !== tabId || !current?.run_id) return
    // A capture that failed still owns a run on the server, so it has to be closed as well.
    if (!active && stored?.status !== 'capturing' && stored?.status !== 'error') return
    await stopRun()
  }

  return {
    read,
    start: ({ meetingId, tabId }) => serialize(() => startRun({ meetingId, tabId })),
    stop: () => serialize(() => stopRun()),
    progress: input => serialize(() => progressRun(input)),
    fail: message => serialize(() => failRun(message)),
    stopIfTab: tabId => serialize(() => stopIfTabRun(tabId)),
  }
}
