import test from 'node:test'
import assert from 'node:assert/strict'
import { parseSseChunk, openTranscriptStream } from './transcript-stream.js'

// The body of the stream arrives in arbitrary chunks, so the fake reader hands out exactly the
// chunks it is given, one read at a time.
function fakeBody(chunks) {
  const encoder = new TextEncoder()
  const queue = [...chunks]
  return {
    getReader() {
      return {
        async read() {
          if (!queue.length) return { value: undefined, done: true }
          return { value: encoder.encode(queue.shift()), done: false }
        },
        async cancel() {},
        releaseLock() {},
      }
    },
  }
}

function environment({ chunks = [], status = 200, freshToken = 'token-2' } = {}) {
  const calls = []
  const segments = []
  const ends = []
  const errors = []
  let tokenRequests = 0
  const fetchImpl = async (url, options) => {
    calls.push({ url, authorization: options.headers.Authorization })
    return { ok: status >= 200 && status < 300, status, body: fakeBody(chunks) }
  }
  async function requestToken() {
    tokenRequests += 1
    return freshToken
  }
  async function open(overrides = {}) {
    await openTranscriptStream({
      url: 'https://api.test/transcript',
      token: 'token-1',
      onSegment: segment => segments.push(segment),
      onEnd: () => ends.push(true),
      onError: message => errors.push(message),
      requestToken,
      fetchImpl,
      ...overrides,
    })
  }
  return { calls, segments, ends, errors, fetchImpl, requestToken, open, tokenRequests: () => tokenRequests }
}

test('parses segment and end events', () => {
  const events = parseSseChunk('event: segment\ndata: {"seq":1}\n\nevent: end\ndata: {"run_id":"x"}\n\n')
  assert.equal(events.length, 2)
  assert.equal(events[0].event, 'segment')
  assert.equal(events[0].data, '{"seq":1}')
  assert.equal(events[1].event, 'end')
})

test('ignores unknown events and blank lines', () => {
  const events = parseSseChunk('event: ping\ndata: {}\n\n\n')
  assert.deepEqual(events, [])
})

test('ignores an incomplete trailing event', () => {
  const events = parseSseChunk('event: segment\ndata: {"seq":1}\n\ndata: partial')
  assert.equal(events.length, 1)
})

test('a segment split across two reads is delivered once its block is complete', async () => {
  const env = environment({
    chunks: ['event: segment\ndata: {"seq":1,"text":"halo"}\n', '\nevent: end\ndata: {"run_id":"run-1"}\n\n'],
  })
  await env.open()
  assert.deepEqual(env.segments, [{ seq: 1, text: 'halo' }])
  assert.equal(env.ends.length, 1)
})

test('the end event closes the stream', async () => {
  const env = environment({ chunks: ['event: end\ndata: {"run_id":"run-1"}\n\n'] })
  await env.open()
  assert.equal(env.ends.length, 1)
  assert.equal(env.errors.length, 0)
})

test('the error event reports the provider failure', async () => {
  const env = environment({
    chunks: ['event: error\ndata: {"code":"transcript_error","message":"Transkripsi gagal."}\n\n'],
  })
  await env.open()
  assert.equal(env.errors.length, 1)
  assert.equal(env.errors[0], 'Transkripsi gagal.')
})

test('an expired token is refreshed once and a second refusal fails the stream', async () => {
  const env = environment({ status: 401 })
  await env.open()
  assert.equal(env.tokenRequests(), 1, 'token baru diminta tepat satu kali')
  assert.deepEqual(env.calls.map(call => call.authorization), ['Bearer token-1', 'Bearer token-2'])
  assert.equal(env.errors.length, 1, 'percobaan kedua yang ditolak dilaporkan sebagai kegagalan')
})

test('a refused response is reported without reading a body', async () => {
  const env = environment({ status: 500 })
  await env.open()
  assert.equal(env.calls.length, 1)
  assert.equal(env.errors.length, 1)
  assert.match(env.errors[0], /500/)
})
