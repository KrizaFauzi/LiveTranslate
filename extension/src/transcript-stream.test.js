import test from 'node:test'
import assert from 'node:assert/strict'
import { parseSseChunk, openTranscriptStream } from './transcript-stream.js'

const delay = ms => new Promise(resolve => setTimeout(resolve, ms))

// The body of the stream arrives in arbitrary chunks, so the fake reader hands out exactly the
// chunks it was given, one read at a time. A body that holds waits for the caller: only an abort
// ends the wait, like a stream that stays open.
function fakeBody(chunks, { hold = false, signal = null } = {}) {
  const encoder = new TextEncoder()
  const queue = [...chunks]
  const body = {
    readers: 0,
    bodyCancels: 0,
    readerCancels: 0,
    getReader() {
      body.readers += 1
      return {
        async read() {
          if (queue.length) return { value: encoder.encode(queue.shift()), done: false }
          if (!hold) return { value: undefined, done: true }
          return new Promise((_resolve, reject) => {
            signal?.addEventListener('abort', () => reject(new Error('aborted')), { once: true })
          })
        },
        async cancel() { body.readerCancels += 1 },
        releaseLock() {},
      }
    },
    async cancel() { body.bodyCancels += 1 },
  }
  return body
}

function environment({ chunks = [], status = 200, freshToken = 'token-2', hold = false, withoutBody = false } = {}) {
  const calls = []
  const bodies = []
  const segments = []
  const ends = []
  const errors = []
  let tokenRequests = 0
  const fetchImpl = async (url, options) => {
    calls.push({ url, authorization: options.headers.Authorization })
    if (withoutBody) {
      bodies.push(null)
      return { ok: true, status, body: null }
    }
    const body = fakeBody(chunks, { hold, signal: options.signal })
    bodies.push(body)
    return { ok: status >= 200 && status < 300, status, body }
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
  return { calls, bodies, segments, ends, errors, fetchImpl, requestToken, open, tokenRequests: () => tokenRequests }
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

test('a data line keeps the spaces of the value and drops only one space after the colon', () => {
  const events = parseSseChunk('event: segment\ndata: halo  \n\nevent: segment\ndata:  dua\n\n')
  assert.equal(events[0].data, 'halo  ')
  assert.equal(events[1].data, ' dua')
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

test('a stream that ends without an end event is reported as a failure', async () => {
  const env = environment({ chunks: ['event: segment\ndata: {"seq":1,"text":"halo"}\n\n'] })
  await env.open()
  assert.equal(env.ends.length, 0)
  assert.equal(env.errors.length, 1, 'the consumer is not left waiting for an end that never comes')
  assert.match(env.errors[0], /terputus/)
})

test('an expired token is refreshed once and a second refusal fails the stream', async () => {
  const env = environment({ status: 401 })
  await env.open()
  assert.equal(env.tokenRequests(), 1, 'token baru diminta tepat satu kali')
  assert.deepEqual(env.calls.map(call => call.authorization), ['Bearer token-1', 'Bearer token-2'])
  assert.equal(env.errors.length, 1, 'percobaan kedua yang ditolak dilaporkan sebagai kegagalan')
  assert.equal(env.bodies[0].bodyCancels, 1, 'the refused body is cancelled, so the connection closes')
})

test('a refused response is reported without reading a body', async () => {
  const env = environment({ status: 500 })
  await env.open()
  assert.equal(env.calls.length, 1)
  assert.equal(env.errors.length, 1)
  assert.match(env.errors[0], /500/)
  assert.equal(env.bodies[0].readers, 0, 'the body is never read')
  assert.equal(env.bodies[0].bodyCancels, 1, 'the body that is not read is cancelled')
})

test('a failure in the consumer is not reported as a failure of the stream', async () => {
  const env = environment({ chunks: ['event: segment\ndata: {"seq":1}\n\n'] })
  await assert.rejects(() => env.open({ onSegment: () => { throw new Error('kesalahan konsumen') } }), /kesalahan konsumen/)
  assert.equal(env.errors.length, 0, 'a bug of the consumer is not a failure of the stream')
})

test('a response without a body is reported as a failure', async () => {
  const env = environment({ withoutBody: true })
  await env.open()
  assert.equal(env.errors.length, 1, 'the stream without a body cannot be read')
  assert.equal(env.ends.length, 0)
})

test('the abort listener of the caller is removed when the stream ends', async () => {
  const listeners = []
  const signal = {
    aborted: false,
    addEventListener(_name, listener) { listeners.push(listener) },
    removeEventListener(_name, listener) {
      const index = listeners.indexOf(listener)
      if (index >= 0) listeners.splice(index, 1)
    },
  }
  const env = environment({ chunks: ['event: end\ndata: {"run_id":"run-1"}\n\n'] })
  await env.open({ signal })
  assert.equal(listeners.length, 0, 'the listener of the caller does not stay behind')
})

test('aborting the stream stops it without reporting an error', async () => {
  const controller = new AbortController()
  const env = environment({ chunks: ['event: segment\ndata: {"seq":1,"text":"halo"}\n\n'], hold: true })
  const opened = env.open({ signal: controller.signal })
  await delay(20)
  controller.abort()
  await opened
  assert.equal(env.segments.length, 1)
  assert.equal(env.errors.length, 0, 'the caller asked for the end, so it is not a failure')
  assert.equal(env.bodies[0].readerCancels, 1, 'the reader is closed')
})
