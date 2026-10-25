import { TRANSCRIPT_KEY } from './transcript-state.js'

const AUDIO_KEY = 'audio_status'
const OFFSCREEN_URL = 'offscreen.html'
// The transcript has to end before the document offscreen is closed: the final segment of the
// provider travels through the stream of that document. The wait follows the close budget of the
// server, so a slow provider still has time to deliver its final segment.
const TRANSCRIPT_END_TIMEOUT_MS = 12000
const TRANSCRIPT_POLL_MS = 50

const delay = ms => new Promise(resolve => setTimeout(resolve, ms))

// createAudioCapture owns one capture run at a time: the worker decides the meeting, the popup only asks to start.
export function createAudioCapture({ chromeApi, session, credentials, fetchImpl = globalThis.fetch, baseUrl = '' }) {
  let active = null
  let generation = 0
  const identityMatches = (state, input) => Boolean(state?.owner_id && state.run_id && state.owner_id === input?.owner_id && state.run_id === input?.run_id)
  async function owned(input, allowStopped = false) {
    const current = await read()
    if (!identityMatches(current, input) || (!allowStopped && current.status === 'stopped')) return false
    const auth = await credentials()
    const latest = await read()
    return auth?.userId === current.owner_id && identityMatches(latest, input) && (allowStopped || latest.status !== 'stopped')
  }
  async function token(input) {
    const ticket = generation
    if (!await owned(input)) return null
    const auth = await credentials()
    const latest = await read()
    if (ticket !== generation || auth?.userId !== input.owner_id || !identityMatches(latest, input) || latest.status === 'stopped') return null
    return auth.accessToken
  }
  async function clear() {
    generation += 1
    active = null
    await session.remove(AUDIO_KEY)
    await session.remove(TRANSCRIPT_KEY)
  }
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

  // The document offscreen holds the audio context of the capture. Closing the document closes
  // the context with it, so a capture that never starts leaves nothing open.
  async function closeOffscreen(runId) {
    const ownerId = active?.owner_id ?? (await read())?.owner_id
    await toOffscreen('audio.capture.stop', runId ? { run_id: runId, owner_id: ownerId } : {}).catch(() => {})
    try {
      await chromeApi.offscreen?.closeDocument?.()
    } catch {
      // Closing an absent document is not an error for the caller.
    }
  }

  async function stopRun() {
    const stored = await read()
    const current = active ?? stored
    if (!current?.run_id) return stored
    // A stop is acknowledged only after browser audio has drained and the server confirms the run
    // terminal. Retryable failures retain owner/run state so a later stop can finish the occurrence.
    try {
      await toOffscreen('audio.capture.flush', { run_id: current.run_id, owner_id: current.owner_id })
    } catch (error) {
      await publish({ ...stored, ...current, status: 'stop_failed', error: error.message })
      throw error
    }
    const auth = await credentials().catch(() => null)
    if (auth?.userId !== current.owner_id) {
      await closeOffscreen(current.run_id)
      active = null
      return publish({ ...stored, ...current, status: 'stop_failed', stop_acknowledged: false })
    }
    let stopped
    try {
      stopped = await api(`/api/audio/runs/${current.run_id}/stop`, auth.accessToken)
    } catch (error) {
      await publish({ ...stored, ...current, status: 'stop_failed', error: error.message })
      throw error
    }
    await waitForTranscriptEnd(current.run_id)
    await closeOffscreen(current.run_id)
    active = null
    return publish({
      ...stored, ...current,
      ...(stopped?.run ? storageMetadata(stopped.run) : {}),
      ...(stopped?.storage ? { storage: stopped.storage } : {}),
      status: 'stopped', stop_acknowledged: true, stopped_at: new Date().toISOString(),
    })
  }

  // waitForTranscriptEnd waits until the transcript of the run ended. The final segment of the
  // provider arrives before the end of the stream, so the transcript is complete when the document
  // offscreen is closed.
  async function waitForTranscriptEnd(runId, timeoutMs = TRANSCRIPT_END_TIMEOUT_MS) {
    const deadline = Date.now() + timeoutMs
    while (Date.now() < deadline) {
      const state = (await session.get(TRANSCRIPT_KEY))[TRANSCRIPT_KEY]
      // A run without a transcript has nothing to wait for.
      if (!state || state.run_id !== runId) return
      if (state.status === 'transcribed' || state.status === 'transcript_error') return
      await delay(TRANSCRIPT_POLL_MS)
    }
  }

  function storageMetadata(run) {
    return { save_transcript: run.save_transcript === true, transcript_state: run.transcript_state ?? 'disabled', transcript_expires_at: run.transcript_expires_at ?? null }
  }
  async function sameOwner(ownerId, ticket) {
    const auth = await credentials()
    if (ticket !== generation || auth?.userId !== ownerId) throw new Error('Sesi sudah berubah.')
  }

  async function startRun({ meetingId, occurrenceId, tabId }) {
    const ticket = generation
    const stored = await read()
    if (stored?.run_id && stored.status !== 'stopped') {
      const currentAuth = await credentials()
      if (stored.owner_id !== currentAuth?.userId) {
        await stopRun()
        await clear()
        throw new Error('Sesi sudah berubah. Mulai capture kembali.')
      }
      // The stored run also owns the offscreen document after a worker restart.
      if (stored.status === 'error') await stopRun()
      else return stored
    }
    if (!meetingId || !occurrenceId || !tabId) throw new Error('Belum ada meeting occurrence aktif yang terdaftar.')
    const auth = await credentials()
    if (!auth) {
      const error = new Error('Masuk kembali untuk mulai capture.')
      error.status = 401
      throw error
    }
    // The document offscreen has to exist before it can report its sample rate.
    let run = null, storage = null
    try {
      await ensureOffscreen()
      const sampleRate = await probeSampleRate()
      await sameOwner(auth.userId, ticket)
      const created = await api('/api/audio/runs', auth.accessToken, { meeting_id: meetingId, occurrence_id: occurrenceId, sample_rate: sampleRate })
      run = created.run
      storage = created.storage ?? null
      await sameOwner(auth.userId, ticket)
      const streamId = await chromeApi.tabCapture.getMediaStreamId({ targetTabId: tabId })
      await sameOwner(auth.userId, ticket)
      await toOffscreen('audio.capture.start', {
        run_id: run.id,
        owner_id: auth.userId,
        stream_id: streamId,
        token: auth.accessToken,
        base_url: baseUrl,
        sample_rate: sampleRate,
      })
      await sameOwner(auth.userId, ticket)
    } catch (error) {
      // The run may exist: close it again on the server.
      if (run) await api(`/api/audio/runs/${run.id}/stop`, auth.accessToken).catch(() => {})
      // The document and its audio context stay closed when the start fails.
      await closeOffscreen()
      throw error
    }
    active = { owner_id: auth.userId, run_id: run.id, meeting_id: meetingId, occurrence_id: occurrenceId, tab_id: tabId }
    return publish({
      status: 'capturing',
      owner_id: auth.userId,
      ...storageMetadata(run),
      ...(storage ? { storage } : {}),
      run_id: run.id,
      meeting_id: meetingId,
      occurrence_id: occurrenceId,
      tab_id: tabId,
      started_at: run.started_at,
      chunks: 0,
      bytes: 0,
    })
  }

  async function progressRun(input) {
    const current = await read()
    if (!await owned(input)) return current
    // Counters cannot change identity, consent, or authoritative storage metadata.
    const { chunks, bytes, dropped } = input
    const status = current.status === 'error' || current.status === 'stopped' ? current.status : 'capturing'
    return publish({ ...current, chunks, bytes, dropped, status })
  }

  async function failRun(message, options = {}) {
    if (!await owned(options)) return read()
    const { source } = options
    if (source === 'transcript') {
      // The transcription failed: the capture stops, so no audio is recorded for a run that has no
      // transcript. The run is closed on the server and the document is closed with it.
      return stopRun()
    }
    const stored = await read()
    if (source === 'terminal') {
      // The backend refused the run for good: the capture stops and the run is closed on the
      // server, so no audio of a dead run is sent anymore. A run that is already stopped keeps its
      // status: the refusal belongs to the run that is closed.
      if (stored?.status === 'stopped') return stored
      const failed = active ?? stored
      // The document is closed first: the capture of a dead run stops right away.
      await closeOffscreen(failed?.run_id)
      active = null
      const state = { ...(stored ?? {}) }
      delete state.run_id
      const published = await publish({ ...state, ...(failed ?? {}), status: 'error', message, failed_at: new Date().toISOString() })
      // The run is closed on the server, so the backend does not keep an open run.
      const auth = await credentials()
      if (auth?.userId === failed?.owner_id && failed?.run_id) await api(`/api/audio/runs/${failed.run_id}/stop`, auth.accessToken).catch(() => {})
      return published
    }
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

  async function storageRun(run, storage) {
    const current = await read()
    if (!storage || !run || !current || run.id !== current.run_id || run.meeting_id !== current.meeting_id || !await owned(current, true)) return current
    if (['deleted', 'expired'].includes(current.storage?.state) && storage.state !== current.storage.state) return current
    return publish({ ...current, transcript_state: run.transcript_state, storage })
  }

  return {
    read,
    start: input => serialize(() => startRun(input)),
    stop: () => serialize(() => stopRun()),
    progress: input => serialize(() => progressRun(input)),
    fail: (message, options) => serialize(() => failRun(message, options)),
    stopIfTab: tabId => serialize(() => stopIfTabRun(tabId)),
    token,
    storage: (run, storage) => serialize(() => storageRun(run, storage)),
    clear: () => serialize(clear),
    invalidate: () => { generation += 1 },
  }
}
