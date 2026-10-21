import test from 'node:test'
import assert from 'node:assert/strict'
import { createAudioCapture } from './audio-capture.js'

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

function chrome({ captureResponse = null } = {}) {
  const created = []
  const messages = []
  let document = false
  return {
    created,
    messages,
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
          if (captureResponse && message.type === 'audio.capture.start') return captureResponse
          return { ok: true }
        },
      },
    },
  }
}

const run = { id: 'bbbbbbbb-2222-4333-8444-555555555555', meeting_id: 'aaaaaaaa-2222-4333-8444-555555555555', started_at: '2026-10-21T00:00:00Z' }
const meetingId = 'aaaaaaaa-2222-4333-8444-555555555555'

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
  const status = await capture.start({ meetingId, tabId: 7 })
  assert.equal(status.status, 'capturing')
  assert.equal(status.run_id, run.id)
  assert.equal(status.chunks, 0)
  assert.equal(requests.length, 1)
  assert.equal(requests[0][0], 'https://api.test/api/audio/runs')
  assert.equal(JSON.parse(requests[0][1].body).meeting_id, meetingId)
  assert.equal(c.created.length, 1)
  assert.equal(c.created[0].reasons[0], 'USER_MEDIA')
  assert.equal(c.messages[0].type, 'audio.capture.start')
  assert.equal(c.messages[0].input.stream_id, 'stream-7')
  assert.equal(c.api.storage.session.values.audio_status.status, 'capturing')
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
  await assert.rejects(() => capture.start({ meetingId, tabId: 7 }), /Masuk kembali/)
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
  await capture.start({ meetingId, tabId: 7 })
  await capture.stop()
  await capture.start({ meetingId, tabId: 8 })
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
  await capture.start({ meetingId, tabId: 7 })
  const status = await capture.stop()
  assert.equal(status.status, 'stopped')
  assert.equal(requests[1][0], `https://api.test/api/audio/runs/${run.id}/stop`)
  assert.ok(c.messages.some(message => message.type === 'audio.capture.stop'))
  assert.equal(c.api.storage.session.values.audio_status.status, 'stopped')
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
  await capture.start({ meetingId, tabId: 7 })
  await capture.progress({ chunks: 3, bytes: 2048 })
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
  await capture.start({ meetingId, tabId: 7 })
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
  await capture.start({ meetingId, tabId: 7 })
  await capture.fail('Gagal mengirim audio ke server.')
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
  await capture.start({ meetingId, tabId: 7 })
  await capture.fail('Gagal mengirim audio ke server.')
  await capture.progress({ chunks: 3, bytes: 2048 })
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
  await assert.rejects(() => capture.start({ meetingId, tabId: 7 }), /Dokumen capture audio gagal memulai/)
  assert.equal(c.api.storage.session.values.audio_status, undefined)
  assert.ok(requests.some(([url]) => url === `https://api.test/api/audio/runs/${run.id}/stop`), 'run server yatim ditutup')
})
