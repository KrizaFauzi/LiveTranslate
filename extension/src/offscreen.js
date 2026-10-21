import { toMonoPcm16, frameSizeFor } from './pcm16.js'

const WORKLET_FILE = 'pcm-worklet.js'
const WORKLET_NODE = 'pcm-capture'
const CHUNK_MILLISECONDS = 100
const PREFERRED_SAMPLE_RATE = 48000
// At most 50 frames (5 seconds of audio) wait for the backend. Newer frames are dropped and
// reported instead of growing the queue without bound.
const MAX_PENDING_FRAMES = 50
const REQUEST_TIMEOUT_MS = 10000
const STOP_DRAIN_TIMEOUT_MS = 5000

export function createOffscreenCapture({
  chromeApi = globalThis.chrome,
  fetchImpl = globalThis.fetch,
  mediaDevices = globalThis.navigator?.mediaDevices,
  AudioContextClass = globalThis.AudioContext,
  WorkletNodeClass = globalThis.AudioWorkletNode,
  requestTimeoutMs = REQUEST_TIMEOUT_MS,
  maxPendingFrames = MAX_PENDING_FRAMES,
  stopDrainTimeoutMs = STOP_DRAIN_TIMEOUT_MS,
} = {}) {
  let stream = null
  let audioContext = null
  let source = null
  let node = null
  let pcm = null
  let run = { id: null, token: null, baseUrl: '' }
  let chunks = 0
  let bytes = 0
  let dropped = 0
  let pending = 0
  let overflowReported = false
  let queue = Promise.resolve()

  // The progress report is fire and forget: it waits for the service worker to wake up, so it
  // must never sit in the upload chain.
  function report() {
    return chromeApi.runtime.sendMessage({ type: 'audio.progress', input: { chunks, bytes, dropped } }).catch(() => {})
  }

  function reportError(message) {
    return chromeApi.runtime.sendMessage({ type: 'audio.error', input: { message } }).catch(() => {})
  }

  async function sendChunk(buffer, token) {
    return fetchImpl(`${run.baseUrl.replace(/\/$/, '')}/api/audio/runs/${run.id}/chunks`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/octet-stream', Authorization: `Bearer ${token}` },
      body: buffer,
      cache: 'no-store',
      signal: AbortSignal.timeout(requestTimeoutMs),
    })
  }

  async function upload(buffer) {
    let response
    try {
      response = await sendChunk(buffer, run.token)
    } catch {
      // A network error or a timed out request never reached the backend: the audio has a hole.
      reportError('Gagal mengirim audio ke server.')
      return false
    }
    if (response.status === 401) {
      const refreshed = await chromeApi.runtime.sendMessage({ type: 'audio.token' }).catch(() => null)
      const token = refreshed?.data?.token
      if (!token) {
        reportError('Token tidak dapat diperbarui.')
        return false
      }
      run.token = token
      try {
        response = await sendChunk(buffer, token)
      } catch {
        reportError('Gagal mengirim audio ke server.')
        return false
      }
    }
    if (!response.ok) {
      // Only the status code is reported: the provider response body never reaches the message.
      reportError(`Gagal mengirim audio ke server (${response.status}).`)
      return false
    }
    chunks += 1
    bytes += buffer.byteLength
    report()
    return true
  }

  // The page owns the mono mixdown: it turns the raw channels of every worklet chunk into PCM16
  // and keeps the samples in 100 ms frames before they leave for the backend.
  function createFrameBuffer(frameSize) {
    const storage = new Int16Array(frameSize * 2)
    let size = 0
    function push(samples) {
      const ready = []
      let read = 0
      while (read < samples.length) {
        const count = Math.min(storage.length - size, samples.length - read)
        storage.set(samples.subarray(read, read + count), size)
        size += count
        read += count
        while (size >= frameSize) {
          ready.push(storage.slice(0, frameSize))
          storage.copyWithin(0, frameSize, size)
          size -= frameSize
        }
      }
      return ready
    }
    function flush() {
      if (size === 0) return []
      const tail = storage.slice(0, size)
      size = 0
      return [tail]
    }
    return { push, flush }
  }

  function enqueue(buffer) {
    if (pending >= maxPendingFrames) {
      dropped += 1
      if (!overflowReported) {
        overflowReported = true
        reportError('Sebagian audio dibuang karena server lambat.')
      }
      report()
      return
    }
    pending += 1
    queue = queue
      .then(() => upload(buffer))
      .catch(() => {})
      .then(() => {
        pending -= 1
        if (pending === 0) overflowReported = false
      })
  }

  function onFrames(event) {
    const channels = event.data?.channels
    const count = event.data?.frames
    if (!pcm || !channels?.length || !count) return
    for (const chunk of pcm.push(toMonoPcm16(channels, count))) {
      enqueue(chunk.buffer)
    }
  }

  async function ensureAudioContext(sampleRate) {
    if (audioContext) return audioContext
    try {
      audioContext = new AudioContextClass({ sampleRate: sampleRate ?? PREFERRED_SAMPLE_RATE })
    } catch {
      // Some browsers refuse an unsupported rate. Chrome accepts it and silently uses the device
      // rate, so the effective rate is always read from the context, never from the request.
      audioContext = new AudioContextClass()
    }
    return audioContext
  }

  async function release() {
    if (node) {
      node.port.onmessage = null
      node.port.close()
      node.disconnect()
    }
    source?.disconnect()
    node = null
    source = null
    await audioContext?.close()
    audioContext = null
    stream?.getTracks().forEach(track => track.stop())
    stream = null
  }

  async function start(input) {
    if (node) return
    run = { id: input.run_id, token: input.token, baseUrl: input.base_url || '' }
    stream = await mediaDevices.getUserMedia({
      audio: { mandatory: { chromeMediaSource: 'tab', chromeMediaSourceId: input.stream_id } },
    })
    try {
      const context = await ensureAudioContext(input.sample_rate)
      await context.audioWorklet.addModule(chromeApi.runtime.getURL(WORKLET_FILE))
      pcm = createFrameBuffer(frameSizeFor(context.sampleRate, CHUNK_MILLISECONDS))
      source = context.createMediaStreamSource(stream)
      node = new WorkletNodeClass(context, WORKLET_NODE)
      node.port.onmessage = onFrames
      source.connect(node)
      node.connect(context.destination)
      await context.resume()
    } catch (error) {
      // A failed start must not leave the capture stream or the audio context open.
      await release()
      reportError(`Gagal memulai capture audio: ${error.message}`)
      throw error
    }
  }

  async function drain() {
    let timer
    const timeout = new Promise(resolve => { timer = setTimeout(resolve, stopDrainTimeoutMs) })
    await Promise.race([queue, timeout])
    clearTimeout(timer)
  }

  async function stop() {
    if (!node && !audioContext) return
    // The worklet keeps up to 100 ms of audio that never leaves the page. Live transcription
    // accepts that loss: the transcript only misses the last fraction of a second.
    if (node) {
      node.port.onmessage = null
      node.port.close()
      node.disconnect()
    }
    node = null
    if (pcm) {
      for (const chunk of pcm.flush()) enqueue(chunk.buffer)
      pcm = null
    }
    await release()
    await drain()
  }

  function handleMessage(message, respond) {
    if (message?.type === 'audio.capture.probe') {
      ensureAudioContext()
        .then(context => respond({ ok: true, sample_rate: context.sampleRate }))
        .catch(error => respond({ ok: false, error: { message: error.message } }))
      return true
    }
    if (message?.type === 'audio.capture.start') {
      start(message.input)
        .then(() => respond({ ok: true, sample_rate: audioContext.sampleRate }))
        .catch(error => respond({ ok: false, error: { message: error.message } }))
      return true
    }
    if (message?.type === 'audio.capture.stop') {
      stop().then(() => respond({ ok: true })).catch(error => respond({ ok: false, error: { message: error.message } }))
      return true
    }
    return false
  }

  return { start, stop, handleMessage }
}

// The document bootstraps the capture; the tests import the factory above.
if (globalThis.chrome?.runtime) {
  const capture = createOffscreenCapture({ chromeApi: globalThis.chrome })
  globalThis.chrome.runtime.onMessage.addListener((message, sender, respond) => capture.handleMessage(message, respond))
}
