import test from 'node:test'
import assert from 'node:assert/strict'
import { createAudioCapture } from './audio-capture.js'

const delay = ms => new Promise(resolve => setTimeout(resolve, ms))

function storage() {
  const values = {}
  return {
    values,
    async get(key) { return { [key]: structuredClone(values[key]) } },
    async set(update) { Object.assign(values, structuredClone(update)) },
    async remove(key) { delete values[key] },
    async setAccessLevel() {},
  }
}

function chrome({ captureResponse = null, probeResponse = { ok: true, sample_rate: 48000 } } = {}) {
  const created = []
  const messages = []
  let document = false
  return {
    created,
    messages,
    get open() { return document },
    api: {
      storage: { session: storage() },
      tabCapture: { getMediaStreamId: async ({ targetTabId }) => `stream-${targetTabId}` },
      offscreen: {
        async hasDocument() { return document },
        async createDocument(options) { document = true; created.push(options) },
        async closeDocument() { document = false },
      },
      runtime: {
        async sendMessage(message) {
          messages.push(message)
          if (message.type === 'audio.capture.probe') return probeResponse
          if (captureResponse && message.type === 'audio.capture.start') return captureResponse
          return { ok: true }
        },
      },
    },
  }
}

const run = { id: 'bbbbbbbb-2222-4333-8444-555555555555', meeting_id: 'aaaaaaaa-2222-4333-8444-555555555555', started_at: '2026-10-21T00:00:00Z' }
const meetingId = 'aaaaaaaa-2222-4333-8444-555555555555'
const occurrenceId = 'abababab-2222-4333-8444-555555555555'

test('start creates a run, captures the tab and publishes the capture status', async () => {
  const c = chrome()
  const requests = []
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, options) => { requests.push([url, options]); return Response.json({ run }) },
    baseUrl: 'https://api.test',
  })
  const status = await capture.start({ meetingId, occurrenceId, tabId: 7 })
  assert.equal(status.status, 'capturing')
  assert.equal(status.run_id, run.id)
  assert.equal(status.chunks, 0)
  assert.equal(requests.length, 1)
  assert.equal(requests[0][0], 'https://api.test/api/audio/runs')
  assert.equal(JSON.parse(requests[0][1].body).meeting_id, meetingId)
  assert.equal(JSON.parse(requests[0][1].body).occurrence_id, occurrenceId)
  assert.equal(c.created.length, 1)
  assert.equal(c.created[0].reasons[0], 'USER_MEDIA')
  assert.deepEqual(c.messages.map(message => message.type), ['audio.capture.probe', 'audio.capture.start'])
  assert.equal(c.messages[1].input.stream_id, 'stream-7')
  assert.equal(c.api.storage.session.values.audio_status.status, 'capturing')
})

test('the run is created with the sample rate that the capture reports', async () => {
  const c = chrome({ probeResponse: { ok: true, sample_rate: 44100 } })
  const requests = []
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, options) => { requests.push([url, options]); return Response.json({ run }) },
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  assert.equal(c.messages[0].type, 'audio.capture.probe', 'the sample rate is asked before the run is created')
  assert.equal(JSON.parse(requests[0][1].body).sample_rate, 44100)
  const start = c.messages.find(message => message.type === 'audio.capture.start')
  assert.equal(start.input.sample_rate, 44100, 'the capture uses the rate of the run')
})

test('a start without a usable sample rate creates no run', async () => {
  const c = chrome({ probeResponse: { ok: true } })
  const requests = []
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, options) => { requests.push([url, options]); return Response.json({ run }) },
    baseUrl: 'https://api.test',
  })
  await assert.rejects(() => capture.start({ meetingId, occurrenceId, tabId: 7 }), /Sample rate/)
  assert.equal(requests.length, 0, 'tanpa sample rate tidak ada run yang dibuat')
})

test('start refuses to run without a registered meeting or a session', async () => {
  const c = chrome()
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => null,
    fetchImpl: async () => { throw new Error('network must not be used') },
    baseUrl: 'https://api.test',
  })
  await assert.rejects(() => capture.start({ meetingId: null, tabId: 7 }), /Belum ada meeting/)
  await assert.rejects(() => capture.start({ meetingId, occurrenceId, tabId: 7 }), /Masuk kembali/)
})

test('the offscreen document is created once and reused', async () => {
  const c = chrome()
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async () => Response.json({ run }),
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  await capture.stop()
  await capture.start({ meetingId, occurrenceId, tabId: 8 })
  assert.equal(c.created.length, 2)
})

test('stop closes the run on the server and marks the capture stopped', async () => {
  const c = chrome()
  const requests = []
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, options) => { requests.push([url, options]); return Response.json({ run }) },
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  const status = await capture.stop()
  assert.equal(status.status, 'stopped')
  assert.equal(requests[1][0], `https://api.test/api/audio/runs/${run.id}/stop`)
  assert.ok(c.messages.some(message => message.type === 'audio.capture.stop'))
  assert.equal(c.api.storage.session.values.audio_status.status, 'stopped')
})

test('stop failure remains explicit and retryable without finishing local state', async () => {
  const c = chrome()
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async url => url.endsWith('/stop')
      ? Response.json({ error: { message: 'stop unavailable' } }, { status: 503 })
      : Response.json({ run }),
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  await assert.rejects(capture.stop(), /stop unavailable/)
  assert.equal(c.api.storage.session.values.audio_status.status, 'stop_failed')
  assert.ok(!c.messages.some(message => message.type === 'audio.capture.stop'))
})

test('stop drains browser audio before closing the backend run', async () => {
  const c = chrome()
  const timeline = []
  const sendMessage = c.api.runtime.sendMessage
  c.api.runtime.sendMessage = async message => {
    timeline.push(message.type)
    return sendMessage(message)
  }
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async url => { timeline.push(`request ${url}`); return Response.json({ run }) },
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  await capture.stop()
  const flushMessage = timeline.indexOf('audio.capture.flush')
  const stopRequest = timeline.findIndex(entry => entry.startsWith('request') && entry.endsWith('/stop'))
  const closeMessage = timeline.indexOf('audio.capture.stop')
  assert.ok(flushMessage >= 0, 'the recorder flushes queued audio')
  assert.ok(stopRequest > flushMessage, 'the backend closes only after the browser queue drains')
  assert.ok(closeMessage > stopRequest, 'the transcript stays open until backend finalization')
})

test('the document is closed only after the transcript ended', async () => {
  const c = chrome()
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async () => Response.json({ run }),
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  // The transcript of the run is still running: the final segment of the provider is on its way.
  c.api.storage.session.values.transcript_state = { run_id: run.id, status: 'transcribing', last_seq: 1, finals: [], partial: null }
  const stopped = capture.stop()
  await delay(20)
  assert.ok(!c.messages.some(message => message.type === 'audio.capture.stop'), 'the document stays open while the transcript runs')
  c.api.storage.session.values.transcript_state = { run_id: run.id, status: 'transcribed', last_seq: 2, finals: [], partial: null }
  await stopped
  assert.ok(c.messages.some(message => message.type === 'audio.capture.stop'), 'the document is closed after the transcript ended')
})

test('a terminal refusal stops the capture and closes the run on the server', async () => {
  const c = chrome()
  const requests = []
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, options) => { requests.push([url, options]); return Response.json({ run }) },
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  const status = await capture.fail('Gagal mengirim audio ke server (409).', { owner_id: 'user', run_id: run.id, source: 'terminal' })
  assert.equal(status.status, 'error', 'the refusal is shown to the user')
  assert.match(status.message, /409/)
  assert.ok(c.messages.some(message => message.type === 'audio.capture.stop'), 'the recorder is stopped')
  assert.ok(requests.some(([url]) => url === `https://api.test/api/audio/runs/${run.id}/stop`), 'the run is closed on the server')
})

test('progress updates the published counters', async () => {
  const c = chrome()
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async () => Response.json({ run }),
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  await capture.progress({ owner_id: 'user', run_id: run.id, chunks: 3, bytes: 2048 })
  assert.equal(c.api.storage.session.values.audio_status.chunks, 3)
  assert.equal(c.api.storage.session.values.audio_status.bytes, 2048)
})

test('closing the captured tab stops the run', async () => {
  const c = chrome()
  const requests = []
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, options) => { requests.push([url, options]); return Response.json({ run }) },
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  await capture.stopIfTab(9)
  assert.equal(requests.length, 1)
  await capture.stopIfTab(7)
  assert.equal(requests.length, 2)
  assert.equal(requests[1][0], `https://api.test/api/audio/runs/${run.id}/stop`)
})

test('a failed upload keeps the run so the capture can still be stopped', async () => {
  const c = chrome()
  const requests = []
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, options) => { requests.push([url, options]); return Response.json({ run }) },
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  await capture.fail('Gagal mengirim audio ke server.', { owner_id: 'user', run_id: run.id })
  const failed = await capture.read()
  assert.equal(failed.status, 'error')
  assert.equal(failed.run_id, run.id)
  assert.equal(failed.message, 'Gagal mengirim audio ke server.')
  const stopped = await capture.stop()
  assert.equal(stopped.status, 'stopped')
  assert.ok(requests.some(([url]) => url === `https://api.test/api/audio/runs/${run.id}/stop`), 'run server ditutup')
})

test('progress never overwrites a failed status', async () => {
  const c = chrome()
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async () => Response.json({ run }),
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  await capture.fail('Gagal mengirim audio ke server.', { owner_id: 'user', run_id: run.id })
  await capture.progress({ owner_id: 'user', run_id: run.id, chunks: 3, bytes: 2048 })
  const status = await capture.read()
  assert.equal(status.status, 'error')
  assert.equal(status.chunks, 3)
  assert.equal(status.bytes, 2048)
})

test('a start refused by the offscreen document never publishes a capturing status', async () => {
  const c = chrome({ captureResponse: { ok: false, error: { message: 'Dokumen capture audio gagal memulai.' } } })
  const requests = []
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, options) => { requests.push([url, options]); return Response.json({ run }) },
    baseUrl: 'https://api.test',
  })
  await assert.rejects(() => capture.start({ meetingId, occurrenceId, tabId: 7 }), /Dokumen capture audio gagal memulai/)
  assert.equal(c.api.storage.session.values.audio_status, undefined)
  assert.ok(requests.some(([url]) => url === `https://api.test/api/audio/runs/${run.id}/stop`), 'run server yatim ditutup')
})

test('a failure and a progress report at the same time keep the failed status', async () => {
  for (const order of ['fail-first', 'progress-first']) {
    const c = chrome()
    const capture = createAudioCapture({
      chromeApi: c.api,
      session: c.api.storage.session,
      credentials: async () => ({ userId: 'user', accessToken: 'token' }),
      fetchImpl: async () => Response.json({ run }),
      baseUrl: 'https://api.test',
    })
    await capture.start({ meetingId, occurrenceId, tabId: 7 })
    const fail = () => capture.fail('Gagal mengirim audio ke server.', { owner_id: 'user', run_id: run.id })
    const progress = () => capture.progress({ owner_id: 'user', run_id: run.id, chunks: 3, bytes: 2048 })
    await Promise.all(order === 'fail-first' ? [fail(), progress()] : [progress(), fail()])
    const status = await capture.read()
    assert.equal(status.status, 'error', order)
    assert.equal(status.message, 'Gagal mengirim audio ke server.', order)
    assert.equal(status.chunks, 3, order)
  }
})

test('a new capture closes a failed run that only the stored state knows about', async () => {
  const c = chrome()
  const requests = []
  let created = 0
  const options = {
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, request) => {
      requests.push([url, request])
      if (url.endsWith('/stop')) return Response.json({ run })
      created += 1
      return Response.json({ run: { ...run, id: `run-${created}` } })
    },
    baseUrl: 'https://api.test',
  }
  const first = createAudioCapture(options)
  await first.start({ meetingId, occurrenceId, tabId: 7 })
  await first.fail('Gagal mengirim audio ke server.', { owner_id: 'user', run_id: 'run-1' })
  const second = createAudioCapture(options)
  await second.start({ meetingId, occurrenceId, tabId: 7 })
  assert.ok(requests.some(([url]) => url === 'https://api.test/api/audio/runs/run-1/stop'), 'run gagal ditutup di server')
  const status = await second.read()
  assert.equal(status.status, 'capturing')
  assert.equal(status.run_id, 'run-2')
})

test('a failed run is closed when its tab closes, even after a worker restart', async () => {
  const c = chrome()
  const requests = []
  let created = 0
  const options = {
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, request) => {
      requests.push([url, request])
      if (url.endsWith('/stop')) return Response.json({ run })
      created += 1
      return Response.json({ run: { ...run, id: `run-${created}` } })
    },
    baseUrl: 'https://api.test',
  }
  const first = createAudioCapture(options)
  await first.start({ meetingId, occurrenceId, tabId: 7 })
  await first.fail('Gagal mengirim audio ke server.', { owner_id: 'user', run_id: 'run-1' })
  const second = createAudioCapture(options)
  await second.stopIfTab(7)
  assert.ok(requests.some(([url]) => url === 'https://api.test/api/audio/runs/run-1/stop'), 'run gagal ditutup di server')
})

test('a late failure after the capture stopped cannot alter its state', async () => {
  const c = chrome()
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async () => Response.json({ run }),
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  await capture.stop()
  await capture.fail('Gagal mengirim audio ke server.', { owner_id: 'user', run_id: 'old-run' })
  const status = await capture.read()
  assert.equal(status.status, 'stopped')
  assert.equal(status.run_id, run.id)
})

test('a failed transcription stops the capture', async () => {
  const c = chrome()
  const requests = []
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, options) => { requests.push([url, options]); return Response.json({ run }) },
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  const status = await capture.fail('Transkripsi gagal.', { owner_id: 'user', run_id: run.id, source: 'transcript' })
  assert.equal(status.status, 'stopped', 'a failed transcription stops the capture')
  assert.ok(requests.some(([url]) => url === `https://api.test/api/audio/runs/${run.id}/stop`), 'the run is closed on the server')
  assert.ok(c.messages.some(message => message.type === 'audio.capture.stop'), 'the recorder is stopped')
})

test('a failed upload does not stop the capture', async () => {
  const c = chrome()
  const requests = []
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, options) => { requests.push([url, options]); return Response.json({ run }) },
    baseUrl: 'https://api.test',
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  const status = await capture.fail('Gagal mengirim audio ke server.', { owner_id: 'user', run_id: run.id })
  assert.equal(status.status, 'error', 'a failed upload degrades the capture, it does not stop it')
  assert.ok(!requests.some(([url]) => url.endsWith('/stop')), 'the run stays open on the server')
  assert.ok(!c.messages.some(message => message.type === 'audio.capture.stop'), 'the recorder keeps running')
})

test('a start that fails after the probe closes the offscreen document', async () => {
  const c = chrome()
  const capture = createAudioCapture({
    chromeApi: c.api,
    session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async () => { throw new Error('backend mati') },
    baseUrl: 'https://api.test',
  })
  await assert.rejects(() => capture.start({ meetingId, occurrenceId, tabId: 7 }), /backend mati/)
  assert.equal(c.open, false, 'the offscreen document is closed')
  assert.ok(c.messages.some(message => message.type === 'audio.capture.stop'), 'the audio context of the document is closed')
  assert.equal(c.api.storage.session.values.audio_status, undefined, 'no capture is published')
})

test('staging is automatic per occurrence and no pre-capture archive choice is sent', async () => {
  const c = chrome(), requests = []
  let created = 0
  const capture = createAudioCapture({
    chromeApi: c.api, session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async (url, options) => {
      requests.push([url, options.body ? JSON.parse(options.body) : null])
      return Response.json({ run: { ...run, id: `run-${++created}`, occurrence_id: occurrenceId, save_transcript: true, transcript_state: 'saving', transcript_expires_at: null } })
    },
  })
  const first = await capture.start({ meetingId, occurrenceId, tabId: 7 })
  assert.equal(requests[0][1].save_transcript, undefined)
  assert.equal(requests[0][1].occurrence_id, occurrenceId)
  assert.equal(first.save_transcript, true, 'server confirms private staging')
  assert.equal(first.owner_id, 'user')
  await capture.stop()
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  assert.equal(requests.at(-1)[1].save_transcript, undefined)
})

test('late owner/run messages cannot change a newer capture or obtain its token', async () => {
  const c = chrome()
  const capture = createAudioCapture({
    chromeApi: c.api, session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async () => Response.json({ run }),
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  for (const input of [{ owner_id: 'other', run_id: run.id }, { owner_id: 'user', run_id: 'old-run' }, {}]) {
    await capture.progress({ ...input, chunks: 999 })
    await capture.fail('late', { ...input, source: 'terminal' })
    assert.equal(await capture.token(input), null)
  }
  assert.equal((await capture.read()).status, 'capturing')
  assert.equal((await capture.read()).chunks, 0)
  assert.equal(await capture.token({ owner_id: 'user', run_id: run.id }), 'token')
})

test('server archive deletion updates availability without stopping capture or changing consent', async () => {
  const c = chrome()
  const capture = createAudioCapture({
    chromeApi: c.api, session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async () => Response.json({ run: { ...run, save_transcript: true, transcript_state: 'saving' } }),
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  await capture.storage({ ...run, save_transcript: true, transcript_state: 'deleted' }, { state: 'deleted', complete: false })
  const status = await capture.read()
  assert.equal(status.status, 'capturing')
  assert.equal(status.save_transcript, true)
  assert.equal(status.storage.state, 'deleted')
  assert.ok(!c.messages.some(message => message.type === 'audio.capture.stop'))
})

test('an account change during the sample-rate probe closes the unused offscreen document', async () => {
  let release, owner = 'user'
  const c = chrome({ probeResponse: new Promise(resolve => { release = resolve }) })
  const capture = createAudioCapture({
    chromeApi: c.api, session: c.api.storage.session,
    credentials: async () => ({ userId: owner, accessToken: 'token' }),
    fetchImpl: async () => { throw Error('no run may be created') },
  })
  const starting = capture.start({ meetingId, occurrenceId, tabId: 7 })
  await delay(5)
  owner = 'other'
  release({ ok: true, sample_rate: 48000 })
  await assert.rejects(starting, /Sesi sudah berubah/)
  assert.equal(c.open, false)
})

test('stopped run messages cannot update counters or request another refresh token', async () => {
  const c = chrome()
  const capture = createAudioCapture({
    chromeApi: c.api, session: c.api.storage.session,
    credentials: async () => ({ userId: 'user', accessToken: 'token' }),
    fetchImpl: async () => Response.json({ run }),
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  await capture.stop()
  const identity = { owner_id: 'user', run_id: run.id }
  await capture.progress({ ...identity, chunks: 88 })
  await capture.fail('late failure', identity)
  assert.equal(await capture.token(identity), null)
  assert.equal((await capture.read()).status, 'stopped')
  assert.equal((await capture.read()).chunks, 0)
})

test('credential refresh failure cannot leave a capture or its offscreen document running during stop', async () => {
  const c = chrome()
  let revoked = false
  const capture = createAudioCapture({
    chromeApi: c.api, session: c.api.storage.session,
    credentials: async () => {
      if (revoked) throw Object.assign(new Error('revoked refresh'), { status: 401 })
      return { userId: 'user', accessToken: 'token' }
    },
    fetchImpl: async () => Response.json({ run }),
  })
  await capture.start({ meetingId, occurrenceId, tabId: 7 })
  revoked = true
  const stopped = await capture.stop()
  assert.equal(stopped.status, 'stop_failed')
  assert.equal(stopped.stop_acknowledged, false)
  assert.equal(c.open, false)
  assert.ok(c.messages.some(message => message.type === 'audio.capture.stop'))
})
