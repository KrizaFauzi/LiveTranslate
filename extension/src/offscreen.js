import { toMonoPcm16, frameSizeFor } from './pcm16.js'
import { openTranscriptStream } from './transcript-stream.js'
import { applySegment, applyStatus, emptyState, TRANSCRIPT_KEY } from './transcript-state.js'

const WORKLET_FILE = 'pcm-worklet.js'
const WORKLET_NODE = 'pcm-capture'
const CHUNK_MILLISECONDS = 100
const PREFERRED_SAMPLE_RATE = 48000
// At most 50 frames (5 seconds of audio) wait for the backend. Newer frames are dropped and
// reported instead of growing the queue without bound.
const MAX_PENDING_FRAMES = 50
const REQUEST_TIMEOUT_MS = 10000
const TOKEN_TIMEOUT_MS = 5000
const STOP_DRAIN_TIMEOUT_MS = 5000
// A terminal answer of the backend ends the run: the run is closed there, so the capture of that
// run cannot continue. A network error is not terminal: the audio has a hole, but the run lives.
const TERMINAL_STATUSES = new Set([409, 502])
// The failures of the upload are rate limited: a backend that refuses every frame would send one
// message per frame, and the worker does not need that many.
const ERROR_INTERVAL_MS = 5000

export function createOffscreenCapture({
  chromeApi = globalThis.chrome,
  fetchImpl = globalThis.fetch,
  mediaDevices = globalThis.navigator?.mediaDevices,
  AudioContextClass = globalThis.AudioContext,
  WorkletNodeClass = globalThis.AudioWorkletNode,
  requestTimeoutMs = REQUEST_TIMEOUT_MS,
  maxPendingFrames = MAX_PENDING_FRAMES,
  tokenTimeoutMs = TOKEN_TIMEOUT_MS,
  stopDrainTimeoutMs = STOP_DRAIN_TIMEOUT_MS,
  errorIntervalMs = ERROR_INTERVAL_MS,
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
  let transcriptController = null
  let transcriptState = null
  // The transcription failed: the capture stops, so the failures that follow are not reported
  // again.
  let transcriptFailed = false
  // The backend refused the run for good: the capture stops and the worker closes the run.
  let terminal = false
  // Once stop begins no new server request may be scheduled. The separate flush phase first
  // disconnects the recorder and drains already-queued audio while this flag is still false.
  let stopping = false
  // The time of the last failure report, so the failures of a dead backend are rate limited.
  let lastErrorAt = 0
  // Every change of the transcript state is a read-modify-write cycle, so the changes run one
  // after another: a segment never overwrites the one before it.
  let stateQueue = Promise.resolve()

  // The progress report is fire and forget: it waits for the service worker to wake up, so it
  // must never sit in the upload chain.
  function report(target = run) {
    if (target !== run) return Promise.resolve()
    return chromeApi.runtime.sendMessage({ type: 'audio.progress', input: { owner_id: target.ownerId, run_id: target.id, chunks, bytes, dropped } }).catch(() => {})
  }

  // reportError sends a failure to the worker. The source tells the worker what failed: a failed
  // upload only degrades the capture, a failed transcription stops it, and a terminal answer of the
  // backend ends the run.
  //
  // A failure of the upload after the transcription failed is not reported again: the capture is
  // stopping because of the transcription, and the worker already knows about it.
  //
  // The failures of the upload are rate limited: a backend that refuses every frame would send one
  // message per frame, and the worker does not need that many.
  function reportError(message, source, target = run) {
    if (target !== run) return Promise.resolve()
    if (source !== 'transcript' && transcriptFailed) return Promise.resolve()
    if (source === 'upload' && !allowErrorReport()) return Promise.resolve()
    return chromeApi.runtime.sendMessage({ type: 'audio.error', input: { owner_id: target.ownerId, run_id: target.id, message, source } }).catch(() => {})
  }

  // allowErrorReport keeps the rate limit of the failure reports: the first failure of a run is
  // reported right away, the next ones only after the interval has passed.
  function allowErrorReport() {
    const now = Date.now()
    if (now - lastErrorAt < errorIntervalMs) return false
    lastErrorAt = now
    return true
  }

  // The transcript state lives in the session storage: the popup reads it while the capture runs.
  async function writeState(state) {
    const storage = chromeApi.storage?.session
    if (!storage) return
    await storage.set({ [TRANSCRIPT_KEY]: state }).catch(() => {})
  }

  // updateState applies one change to the transcript state. The changes run one after another, so
  // a segment never overwrites the one before it. A state without a run is not published: nothing
  // was transcribed for it.
  function updateState(change, target = run) {
    stateQueue = stateQueue.then(async () => {
      if (target !== run) return
      transcriptState = change(transcriptState ?? emptyState(target.id, target.ownerId))
      if (!transcriptState.run_id) return
      await writeState(transcriptState)
    }).catch(() => {})
    return stateQueue
  }

  // openTranscript reads the transcript of the run. The stream lives as long as the capture runs
  // and asks for the segments the client already has, so a replayed backlog is not shown twice.
  function openTranscript(input) {
    const base = (input.base_url || '').replace(/\/$/, '')
    const target = run
    const controller = new AbortController()
    transcriptController = controller
    openTranscriptStream({
      url: `${base}/api/audio/runs/${input.run_id}/transcript?since=${transcriptState?.last_seq ?? 0}`,
      token: input.token,
      signal: controller.signal,
      fetchImpl,
      requestToken: () => requestToken(target),
      onSegment: segment => { updateState(state => applySegment(state, segment), target) },
      onEnd: () => { updateState(state => applyStatus(state, 'transcribed'), target) },
      onError: message => {
        if (target !== run) return
        transcriptFailed = true
        updateState(state => applyStatus(state, 'transcript_error', message), target)
        // The worker owns the run, so it has to learn that the transcription failed: the capture
        // stops with it.
        reportError(message, 'transcript', target)
      },
    }).catch(() => reportError('Gagal membaca transkrip.', 'transcript', target))
  }

  async function sendChunk(buffer, token, target) {
    return fetchImpl(`${target.baseUrl.replace(/\/$/, '')}/api/audio/runs/${target.id}/chunks`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/octet-stream', Authorization: `Bearer ${token}` },
      body: buffer,
      cache: 'no-store',
      signal: AbortSignal.timeout(requestTimeoutMs),
    })
  }

  // The service worker has to answer within the bound: an unanswered request would freeze the
  // upload chain for good.
  async function requestToken(target = run) {
    let timer
    const timeout = new Promise(resolve => { timer = setTimeout(() => resolve(null), tokenTimeoutMs) })
    try {
      const refreshed = await Promise.race([chromeApi.runtime.sendMessage({ type: 'audio.token', input: { owner_id: target.ownerId, run_id: target.id } }), timeout])
      return target === run ? refreshed?.data?.token ?? null : null
    } catch {
      return null
    } finally {
      clearTimeout(timer)
    }
  }

  async function upload(buffer, target) {
    // Queued uploads cannot adopt the identity or refreshed token of a later capture.
    if (target !== run || terminal || stopping) return false
    let response
    try {
      response = await sendChunk(buffer, target.token, target)
      if (target !== run) return false
    } catch {
      // A network error or a timed out request never reached the backend: the audio has a hole,
      // but the run lives, so the capture continues.
      reportError('Gagal mengirim audio ke server.', 'upload', target)
      return false
    }
    if (response.status === 401) {
      const token = await requestToken(target)
      if (target !== run) return false
      if (!token) {
        // The token cannot be refreshed: the run cannot continue, so the answer is terminal.
        await failTerminal('Token tidak dapat diperbarui.', target)
        return false
      }
      target.token = token
      try {
        response = await sendChunk(buffer, token, target)
        if (target !== run) return false
      } catch {
        reportError('Gagal mengirim audio ke server.', 'upload', target)
        return false
      }
    }
    if (!response.ok) {
      // Only the status code is reported: the provider response body never reaches the message.
      const message = `Gagal mengirim audio ke server (${response.status}).`
      if (TERMINAL_STATUSES.has(response.status)) {
        // The backend refused the run for good: the capture stops and the worker closes the run.
        await failTerminal(message, target)
        return false
      }
      reportError(message, 'upload')
      return false
    }
    chunks += 1
    bytes += buffer.byteLength
    report()
    return true
  }

  // failTerminal ends the capture when the backend refuses the run for good. The worker owns the
  // run, so it closes it on the server.
  async function failTerminal(message, target = run) {
    if (target !== run) return
    if (terminal) return
    terminal = true
    // The capture stops: the audio of a dead run is not recorded anymore.
    await release()
    reportError(message, 'terminal', target)
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
    const target = run
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
      .then(() => upload(buffer, target))
      .catch(() => {})
      .then(() => {
        if (target !== run) return
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
    // A new capture starts clean: the failures of the run before it do not silence its own.
    transcriptFailed = false
    terminal = false
    stopping = false
    lastErrorAt = 0
    run = { id: input.run_id, ownerId: input.owner_id, token: input.token, baseUrl: input.base_url || '' }
    chunks = 0; bytes = 0; dropped = 0; pending = 0; queue = Promise.resolve()
    transcriptState = null
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
    // The transcript belongs to one run: a new run empties the state of the old one.
    await updateState(() => emptyState(input.run_id, input.owner_id))
    openTranscript(input)
  }

  async function drain() {
    let timer
    const timeout = new Promise(resolve => { timer = setTimeout(() => resolve(false), stopDrainTimeoutMs) })
    const drained = await Promise.race([queue.then(() => true), timeout])
    clearTimeout(timer)
    if (!drained) throw new Error('Antrean audio belum selesai dikirim. Coba hentikan lagi.')
  }

  async function flush() {
    if (node) {
      node.port.onmessage = null
      node.port.close()
      node.disconnect()
      node = null
    }
    if (pcm) {
      for (const chunk of pcm.flush()) enqueue(chunk.buffer)
      pcm = null
    }
    await release()
    await drain()
  }

  async function stop() {
    const target = run
    await flush()
    stopping = true
    // The transcript remains open through browser-audio drain and backend finalization. This final
    // stop is sent only after the backend has emitted its final transcript event.
    transcriptController?.abort()
    transcriptController = null
    await updateState(state => state.status === 'transcript_error' ? state : applyStatus(state, 'transcribed'), target)
    if (target === run) run = { id: null, token: null, baseUrl: '' }
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
    if (message?.type === 'audio.capture.flush') {
      if (message.input?.run_id && (message.input.run_id !== run.id || message.input.owner_id !== run.ownerId)) {
        respond({ ok: true }); return false
      }
      flush().then(() => respond({ ok: true })).catch(error => respond({ ok: false, error: { message: error.message } }))
      return true
    }
    if (message?.type === 'audio.capture.stop') {
      if (message.input?.run_id && (message.input.run_id !== run.id || message.input.owner_id !== run.ownerId)) {
        respond({ ok: true }); return false
      }
      stop().then(() => respond({ ok: true })).catch(error => respond({ ok: false, error: { message: error.message } }))
      return true
    }
    return false
  }

  return { start, flush, stop, handleMessage }
}

// The document bootstraps the capture; the tests import the factory above.
if (globalThis.chrome?.runtime) {
  const capture = createOffscreenCapture({ chromeApi: globalThis.chrome })
  globalThis.chrome.runtime.onMessage.addListener((message, sender, respond) => capture.handleMessage(message, respond))
}
