import { toMonoPcm16, frameSizeFor } from './pcm16.js'

const WORKLET_FILE = 'pcm-worklet.js'
const WORKLET_NODE = 'pcm-capture'
const CHUNK_MILLISECONDS = 100
const REQUESTED_SAMPLE_RATE = 48000

let stream = null
let audioContext = null
let source = null
let node = null
let pcm = null
let run = { id: null, token: null, baseUrl: '' }
let chunks = 0
let bytes = 0
let queue = Promise.resolve()

function report() {
  return chrome.runtime.sendMessage({ type: 'audio.progress', input: { chunks, bytes } }).catch(() => {})
}

async function sendChunk(buffer, token) {
  return fetch(`${run.baseUrl.replace(/\/$/, '')}/api/audio/runs/${run.id}/chunks`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/octet-stream', Authorization: `Bearer ${token}` },
    body: buffer,
    cache: 'no-store',
  })
}

async function upload(buffer) {
  let response = await sendChunk(buffer, run.token)
  if (response.status === 401) {
    const refreshed = await chrome.runtime.sendMessage({ type: 'audio.token' }).catch(() => null)
    const token = refreshed?.data?.token
    if (!token) return false
    run.token = token
    response = await sendChunk(buffer, token)
  }
  if (!response.ok) return false
  chunks += 1
  bytes += buffer.byteLength
  await report()
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

function onFrames(event) {
  const channels = event.data?.channels
  const count = event.data?.frames
  if (!pcm || !channels?.length || !count) return
  for (const chunk of pcm.push(toMonoPcm16(channels, count))) {
    queue = queue.then(() => upload(chunk.buffer)).catch(() => {})
  }
}

async function ensureAudioContext(sampleRate) {
  if (audioContext) return audioContext
  try {
    audioContext = new AudioContext({ sampleRate: sampleRate ?? REQUESTED_SAMPLE_RATE })
  } catch {
    audioContext = new AudioContext()
  }
  return audioContext
}

async function start(input) {
  if (node) return
  run = { id: input.run_id, token: input.token, baseUrl: input.base_url || '' }
  stream = await navigator.mediaDevices.getUserMedia({
    audio: { mandatory: { chromeMediaSource: 'tab', chromeMediaSourceId: input.stream_id } },
  })
  const context = await ensureAudioContext(input.sample_rate)
  await context.audioWorklet.addModule(chrome.runtime.getURL(WORKLET_FILE))
  pcm = createFrameBuffer(frameSizeFor(context.sampleRate, CHUNK_MILLISECONDS))
  source = context.createMediaStreamSource(stream)
  node = new AudioWorkletNode(context, WORKLET_NODE)
  node.port.onmessage = onFrames
  source.connect(node)
  node.connect(context.destination)
  await context.resume()
}

async function stop() {
  if (!node && !audioContext) return
  if (node) {
    node.port.onmessage = null
    node.port.close()
    node.disconnect()
  }
  source?.disconnect()
  node = null
  source = null
  if (pcm) {
    for (const chunk of pcm.flush()) queue = queue.then(() => upload(chunk.buffer)).catch(() => {})
    pcm = null
  }
  await audioContext?.close()
  audioContext = null
  stream?.getTracks().forEach(track => track.stop())
  stream = null
  await queue
}

chrome.runtime.onMessage.addListener((message, sender, respond) => {
  if (message?.type === 'audio.capture.probe') {
    ensureAudioContext()
      .then(context => respond({ ok: true, sample_rate: context.sampleRate }))
      .catch(error => respond({ ok: false, error: { message: error.message } }))
    return true
  }
  if (message?.type === 'audio.capture.start') {
    start(message.input)
      .then(() => respond({ ok: true, sample_rate: audioContext.sampleRate }))
      .catch(error => {
        chrome.runtime.sendMessage({ type: 'audio.error', input: { message: error.message } }).catch(() => {})
        respond({ ok: false, error: { message: error.message } })
      })
    return true
  }
  if (message?.type === 'audio.capture.stop') {
    stop().then(() => respond({ ok: true })).catch(error => respond({ ok: false, error: { message: error.message } }))
    return true
  }
  return false
})
