import test from 'node:test'
import assert from 'node:assert/strict'
import { createOffscreenCapture } from './offscreen.js'

const FRAME_SAMPLES = 4800
const FRAME_BYTES = FRAME_SAMPLES * 2

const delay = ms => new Promise(resolve => setTimeout(resolve, ms))

function environment({ fetchImpl, contextSampleRate, tokenHangs = false } = {}) {
  const sent = []
  const tracks = []
  const contexts = []
  const nodes = []
  const loaded = []
  const chromeApi = {
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
  return { sent, tracks, contexts, nodes, loaded, fetchImpl, chromeApi, mediaDevices, AudioContextClass: FakeAudioContext, WorkletNodeClass: FakeWorkletNode }
}

function feed(node, samples = FRAME_SAMPLES) {
  const left = new Float32Array(samples).fill(0.5)
  const right = new Float32Array(samples).fill(0.5)
  node.port.onmessage({ data: { type: 'frames', frames: samples, channels: [left, right] } })
}

const input = { run_id: 'run-1', stream_id: 'stream-1', token: 'token-1', base_url: 'https://api.test' }

test('the upload queue stays bounded while the backend is unresponsive', async () => {
  let calls = 0
  const env = environment({ fetchImpl: () => { calls += 1; return new Promise(() => {}) } })
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
    fetchImpl: (url, options) => new Promise((resolve, reject) => {
      calls.push(url)
      // Without a signal the request never times out, so the upload would hang forever.
      if (!options.signal) return
      options.signal.addEventListener('abort', () => reject(new Error('aborted')), { once: true })
    }),
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
  const env = environment({ fetchImpl: async () => new Response('provider secret', { status: 500 }) })
  const capture = createOffscreenCapture(env)
  await capture.start(input)
  feed(env.nodes[0])
  await delay(20)
  const error = env.sent.find(message => message.type === 'audio.error')
  assert.match(error.input.message, /500/)
  assert.doesNotMatch(error.input.message, /provider secret/)
})

test('a successful upload counts the bytes and reports the progress', async () => {
  const env = environment({ fetchImpl: async () => new Response(null, { status: 200 }) })
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
    fetchImpl: async () => { calls.push('chunk'); return new Response(null, { status: 401 }) },
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
