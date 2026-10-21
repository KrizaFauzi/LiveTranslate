import test from 'node:test'
import assert from 'node:assert/strict'
import { createOffscreenCapture } from './offscreen.js'

const FRAME_SAMPLES = 4800
const FRAME_BYTES = FRAME_SAMPLES * 2

const delay = ms => new Promise(resolve => setTimeout(resolve, ms))

// waitFor waits until the state of the capture reaches the expected value.
async function waitFor(predicate, timeoutMs = 500) {
  const started = Date.now()
  while (Date.now() - started < timeoutMs) {
    if (predicate()) return true
    await delay(5)
  }
  return false
}

function storage() {
  const values = {}
  return {
    values,
    async get(key) { return { [key]: structuredClone(values[key]) } },
    async set(update) { Object.assign(values, structuredClone(update)) },
  }
}

// fakeBody hands out the chunks of the transcript stream one read at a time. A body that holds
// waits for the caller: only an abort ends the wait, like a stream that stays open.
function fakeBody(chunks, { hold = false, signal = null } = {}) {
  const encoder = new TextEncoder()
  const queue = [...chunks]
  return {
    getReader() {
      return {
        async read() {
          if (queue.length) return { value: encoder.encode(queue.shift()), done: false }
          if (!hold) return { value: undefined, done: true }
          return new Promise((_resolve, reject) => {
            signal?.addEventListener('abort', () => reject(new Error('aborted')), { once: true })
          })
        },
        async cancel() {},
        releaseLock() {},
      }
    },
    async cancel() {},
  }
}

// brokenBody fails on the first read, like a connection that dies in the middle of the stream.
function brokenBody() {
  return {
    getReader() {
      return {
        async read() { throw new Error('koneksi putus') },
        async cancel() {},
        releaseLock() {},
      }
    },
    async cancel() {},
  }
}

// transcriptFetch serves the transcript stream and the uploads of the audio.
function transcriptFetch(chunks, { hold = false, broken = false } = {}) {
  return async (url, options) => {
    if (!url.includes('/transcript')) return new Response(null, { status: 200 })
    return { ok: true, status: 200, body: broken ? brokenBody() : fakeBody(chunks, { hold, signal: options.signal }) }
  }
}

// chunkFetch answers the uploads of the audio and leaves the transcript stream to itself, so a
// test about the uploads does not see the request of the stream.
function chunkFetch(handler) {
  return async (url, options) => {
    if (url.includes('/transcript')) return { ok: true, status: 200, body: fakeBody(['event: end\ndata: {"run_id":"run-1"}\n\n'], { signal: options.signal }) }
    return handler(url, options)
  }
}

function environment({ fetchImpl, contextSampleRate, tokenHangs = false } = {}) {
  const sent = []
  const tracks = []
  const contexts = []
  const nodes = []
  const loaded = []
  const session = storage()
  const chromeApi = {
    storage: { session },
    runtime: {
      getURL: path => `chrome-extension://test/${path}`,
      sendMessage: message => {
        sent.push(message)
        // A service worker that never answers must not freeze the upload chain.
        if (tokenHangs && message.type === 'audio.token') return new Promise(() => {})
        return Promise.resolve({ ok: true, data: { token: 'fresh-token' } })
      },
      onMessage: { addListener: () => {} },
    },
  }
  const mediaDevices = {
    async getUserMedia() {
      const track = { stopped: false, stop() { this.stopped = true } }
      tracks.push(track)
      return { getTracks: () => [track] }
    },
  }
  class FakeAudioContext {
    constructor(options = {}) {
      this.requested = options.sampleRate
      this.sampleRate = contextSampleRate ?? options.sampleRate ?? 48000
      this.audioWorklet = { addModule: async url => { loaded.push(url) } }
      this.destination = {}
      this.closed = false
      contexts.push(this)
    }
    createMediaStreamSource(stream) { return { stream, connect() {}, disconnect() {} } }
    async resume() {}
    async close() { this.closed = true }
  }
  class FakeWorkletNode {
    constructor(context, name) {
      this.context = context
      this.name = name
      this.port = { onmessage: null, closed: false, close() { this.closed = true } }
      nodes.push(this)
    }
    connect(target) { this.connected = target }
    disconnect() { this.disconnected = true }
  }
  return { sent, tracks, contexts, nodes, loaded, session, fetchImpl, chromeApi, mediaDevices, AudioContextClass: FakeAudioContext, WorkletNodeClass: FakeWorkletNode }
}

function feed(node, samples = FRAME_SAMPLES) {
  const left = new Float32Array(samples).fill(0.5)
  const right = new Float32Array(samples).fill(0.5)
  node.port.onmessage({ data: { type: 'frames', frames: samples, channels: [left, right] } })
}

const input = { run_id: 'run-1', stream_id: 'stream-1', token: 'token-1', base_url: 'https://api.test' }

test('the upload queue stays bounded while the backend is unresponsive', async () => {
  let calls = 0
  const env = environment({ fetchImpl: chunkFetch(() => { calls += 1; return new Promise(() => {}) }) })
  const capture = createOffscreenCapture({ ...env, maxPendingFrames: 5 })
  await capture.start(input)
  for (let index = 0; index < 12; index += 1) feed(env.nodes[0])
  await delay(20)
  assert.equal(calls, 1, 'hanya unggahan pertama yang berjalan')
  const progress = env.sent.filter(message => message.type === 'audio.progress').at(-1)
  assert.equal(progress.input.dropped, 7, 'frame di luar batas dibuang, bukan ditumpuk')
  const error = env.sent.find(message => message.type === 'audio.error')
  assert.match(error.input.message, /dibuang/)
})

test('an unresponsive upload times out and the chain keeps moving', async () => {
  const calls = []
  const env = environment({
    fetchImpl: chunkFetch((url, options) => new Promise((resolve, reject) => {
      calls.push(url)
      // Without a signal the request never times out, so the upload would hang forever.
      if (!options.signal) return
      options.signal.addEventListener('abort', () => reject(new Error('aborted')), { once: true })
    })),
  })
  const capture = createOffscreenCapture({ ...env, requestTimeoutMs: 50 })
  await capture.start(input)
  feed(env.nodes[0])
  await delay(150)
  assert.equal(calls.length, 1)
  const error = env.sent.find(message => message.type === 'audio.error')
  assert.match(error.input.message, /Gagal mengirim audio/)
  feed(env.nodes[0])
  await delay(50)
  assert.equal(calls.length, 2, 'rantai lanjut setelah unggahan gagal')
})

test('a failed upload is reported without leaking the provider response', async () => {
  const env = environment({ fetchImpl: chunkFetch(async () => new Response('provider secret', { status: 500 })) })
  const capture = createOffscreenCapture(env)
  await capture.start(input)
  feed(env.nodes[0])
  await delay(20)
  const error = env.sent.find(message => message.type === 'audio.error')
  assert.match(error.input.message, /500/)
  assert.doesNotMatch(error.input.message, /provider secret/)
})

test('a successful upload counts the bytes and reports the progress', async () => {
  const env = environment({ fetchImpl: chunkFetch(async () => new Response(null, { status: 200 })) })
  const capture = createOffscreenCapture(env)
  await capture.start(input)
  feed(env.nodes[0])
  await delay(20)
  const progress = env.sent.filter(message => message.type === 'audio.progress').at(-1)
  assert.equal(progress.input.chunks, 1)
  assert.equal(progress.input.bytes, FRAME_BYTES)
  assert.equal(progress.input.dropped, 0)
  assert.equal(env.sent.filter(message => message.type === 'audio.error').length, 0)
})

test('a failed start closes the capture stream and the audio context', async () => {
  const env = environment({ fetchImpl: async () => new Response(null, { status: 200 }) })
  env.AudioContextClass = class extends env.AudioContextClass {
    constructor(options) {
      super(options)
      this.audioWorklet = { addModule: async () => { throw new Error('worklet gagal dimuat') } }
    }
  }
  const capture = createOffscreenCapture(env)
  await assert.rejects(() => capture.start(input), /worklet gagal dimuat/)
  assert.equal(env.tracks[0].stopped, true)
  assert.equal(env.contexts[0].closed, true)
  const error = env.sent.find(message => message.type === 'audio.error')
  assert.match(error.input.message, /Gagal memulai capture audio/)
})

test('the reported sample rate is the rate of the audio context', async () => {
  const env = environment({ contextSampleRate: 44100 })
  const capture = createOffscreenCapture(env)
  const response = await new Promise(resolve => {
    assert.equal(capture.handleMessage({ type: 'audio.capture.probe' }, resolve), true)
  })
  assert.equal(response.ok, true)
  assert.equal(response.sample_rate, 44100)
  assert.equal(env.contexts[0].requested, 48000)
})

test('an unanswered token request stops on its own and the chain keeps moving', async () => {
  const calls = []
  const env = environment({
    fetchImpl: chunkFetch(async () => { calls.push('chunk'); return new Response(null, { status: 401 }) }),
    tokenHangs: true,
  })
  const capture = createOffscreenCapture({ ...env, tokenTimeoutMs: 50 })
  await capture.start(input)
  feed(env.nodes[0])
  await delay(120)
  const error = env.sent.find(message => message.type === 'audio.error')
  assert.match(error.input.message, /Token tidak dapat diperbarui/)
  feed(env.nodes[0])
  await delay(30)
  assert.equal(calls.length, 2, 'ran upload lanjut setelah permintaan token tidak dijawab')
})

test('the segments of the run are stored in the session state', async () => {
  const env = environment({
    fetchImpl: transcriptFetch([
      'event: segment\ndata: {"seq":1,"type":"partial","text":"selamat"}\n\n',
      'event: segment\ndata: {"seq":2,"type":"final","text":"selamat pagi"}\n\n',
      'event: end\ndata: {"run_id":"run-1"}\n\n',
    ]),
  })
  const capture = createOffscreenCapture(env)
  await capture.start(input)
  assert.ok(await waitFor(() => env.session.values.transcript_state?.status === 'transcribed'), 'the transcript state is published')
  const state = env.session.values.transcript_state
  assert.equal(state.run_id, 'run-1')
  assert.equal(state.finals.length, 1)
  assert.equal(state.finals[0].text, 'selamat pagi')
  assert.equal(state.partial, null)
  assert.equal(state.last_seq, 2)
})

test('the transcript stream asks for the segments the client already has', async () => {
  const urls = []
  const env = environment({
    fetchImpl: async (url, options) => {
      urls.push(url)
      if (!url.includes('/transcript')) return new Response(null, { status: 200 })
      return { ok: true, status: 200, body: fakeBody(['event: end\ndata: {"run_id":"run-1"}\n\n'], { signal: options.signal }) }
    },
  })
  const capture = createOffscreenCapture(env)
  await capture.start(input)
  assert.ok(await waitFor(() => urls.some(url => url.includes('/transcript'))))
  const transcript = urls.find(url => url.includes('/transcript'))
  assert.equal(transcript, 'https://api.test/api/audio/runs/run-1/transcript?since=0')
})

test('the state of an earlier run is cleared when a new run starts', async () => {
  const env = environment({ fetchImpl: transcriptFetch(['event: end\ndata: {"run_id":"run-1"}\n\n']) })
  env.session.values.transcript_state = { run_id: 'run-lama', status: 'transcribing', last_seq: 9, finals: [{ seq: 9, text: 'lama' }], partial: null }
  const capture = createOffscreenCapture(env)
  await capture.start(input)
  assert.ok(await waitFor(() => env.session.values.transcript_state?.status === 'transcribed'))
  const state = env.session.values.transcript_state
  assert.equal(state.run_id, 'run-1')
  assert.equal(state.finals.length, 0)
})

test('the transcript stream closes when the capture stops', async () => {
  const env = environment({ fetchImpl: transcriptFetch([], { hold: true }) })
  const capture = createOffscreenCapture(env)
  await capture.start(input)
  assert.ok(await waitFor(() => env.session.values.transcript_state?.status === 'waiting'))
  await capture.stop()
  assert.ok(await waitFor(() => env.session.values.transcript_state?.status === 'transcribed'), 'the transcript is marked as finished')
})

test('a broken transcript stream marks the state as failed', async () => {
  const env = environment({ fetchImpl: transcriptFetch([], { broken: true }) })
  const capture = createOffscreenCapture(env)
  await capture.start(input)
  assert.ok(await waitFor(() => env.session.values.transcript_state?.status === 'transcript_error'), 'the failure is published')
  const error = env.sent.find(message => message.type === 'audio.error')
  assert.ok(error, 'the worker learns about the failure')
  assert.match(error.input.message, /Gagal membaca stream transkrip/)
})
