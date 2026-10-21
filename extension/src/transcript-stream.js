// The transcript stream is opened with fetch and read as a stream: EventSource cannot send the
// bearer token that the endpoint requires, and the service worker has no EventSource at all.

// The protocol carries three events: any other event is not ours and is ignored.
const KNOWN_EVENTS = new Set(['segment', 'end', 'error'])

const TOKEN_ERROR = 'Token tidak dapat diperbarui.'
const CONNECT_ERROR = 'Gagal terhubung ke server transkrip.'
const STREAM_ERROR = 'Gagal membaca stream transkrip.'
const STREAM_ENDED_ERROR = 'Stream transkrip terputus.'
const TRANSCRIPT_ERROR = 'Transkripsi gagal.'

// parseSseChunk reads the events of a chunk of the stream. Only a block that ends with a blank
// line is complete, so the trailing block of a chunk is left out: the caller keeps it until the
// rest of it arrives.
export function parseSseChunk(text) {
  const events = []
  const blocks = text.split('\n\n')
  // The trailing block is only complete when the text ends with a blank line.
  if (!text.endsWith('\n\n')) blocks.pop()
  for (const block of blocks) {
    if (!block.trim()) continue
    let name = 'message'
    const data = []
    for (const line of block.split('\n')) {
      if (line.startsWith('event:')) name = line.slice(6).trim()
      else if (line.startsWith('data:')) data.push(dataValue(line))
    }
    if (!data.length) continue
    if (!KNOWN_EVENTS.has(name)) continue
    events.push({ event: name, data: data.join('\n') })
  }
  return events
}

// dataValue reads the value of a data line. The format removes one space after the colon and
// nothing else, so a value that ends with a space keeps it.
function dataValue(line) {
  const value = line.slice('data:'.length)
  return value.startsWith(' ') ? value.slice(1) : value
}

// openTranscriptStream opens the transcript stream of one run and reports every segment, the end
// of the run, and the failure of the stream. The promise settles when the stream ends, so the
// caller awaits it to know that the run is over.
//
// An expired access token is refreshed once: the worker hands out a fresh token and the stream
// opens again. A second refusal is a failure, not a loop.
//
// The caller stops the stream with an abort signal: the request is cancelled and the reader is
// closed without an error report, because the caller asked for it.
//
// Every failure of the stream itself is reported through onError. A failure in the callback of
// the consumer is not a failure of the stream, so it travels to the caller as a rejected promise
// instead of reaching the user.
export async function openTranscriptStream({
  url,
  token,
  onSegment,
  onEnd,
  onError,
  requestToken,
  fetchImpl = globalThis.fetch,
  signal,
}) {
  const controller = new AbortController()
  // The listener is removed when the stream ends, so a caller that keeps its signal does not
  // collect one listener per run.
  let abortListener = null
  if (signal) {
    if (signal.aborted) controller.abort()
    else {
      abortListener = () => controller.abort()
      signal.addEventListener('abort', abortListener, { once: true })
    }
  }
  try {
    await runStream()
  } finally {
    if (abortListener) signal.removeEventListener('abort', abortListener)
  }

  // runStream opens the stream and reads it until the run ends.
  async function runStream() {
    function open(currentToken) {
      return fetchImpl(url, {
        headers: { Authorization: `Bearer ${currentToken}` },
        cache: 'no-store',
        signal: controller.signal,
      })
    }

    // readStream reads the body of the response until the run ends. The body arrives in arbitrary
    // chunks, so the text of the incomplete block is kept until the rest of it arrives.
    async function readStream(body) {
      const decoder = new TextDecoder()
      let pending = ''
      let reader = null
      try {
        try {
          reader = body.getReader()
        } catch {
          // The response has no readable body, so the stream cannot be read.
          onError(STREAM_ERROR)
          return
        }
        for (;;) {
          let value
          try {
            const read = await reader.read()
            if (read.done) break
            value = read.value
          } catch {
            // A broken connection is a failure of the stream, unless the caller closed it.
            if (!controller.signal.aborted) onError(STREAM_ERROR)
            return
          }
          pending += decoder.decode(value, { stream: true })
          const cut = pending.lastIndexOf('\n\n')
          if (cut === -1) continue
          const complete = pending.slice(0, cut + 2)
          pending = pending.slice(cut + 2)
          for (const event of parseSseChunk(complete)) {
            if (event.event === 'segment') {
              let segment
              try {
                segment = JSON.parse(event.data)
              } catch {
                // The payload of the server is broken, so the stream cannot be read.
                onError(STREAM_ERROR)
                return
              }
              // The consumer runs outside the guard of the transport: a failure of the consumer
              // is its own, not a failure of the stream.
              onSegment(segment)
            } else if (event.event === 'end') {
              onEnd()
              return
            } else if (event.event === 'error') {
              onError(errorMessage(event.data))
              return
            }
          }
        }
        // The body ended without an end or an error event: the server cut the stream, so the
        // consumer must not wait for a run that is never over.
        if (!controller.signal.aborted) onError(STREAM_ENDED_ERROR)
      } finally {
        if (reader) {
          await reader.cancel().catch(() => {})
          reader.releaseLock?.()
        }
      }
    }

    let response
    try {
      response = await open(token)
    } catch {
      if (!controller.signal.aborted) onError(CONNECT_ERROR)
      return
    }
    if (response.status === 401) {
      // The refused body is not read: it is cancelled, so the connection does not stay open.
      await cancelBody(response)
      let fresh = null
      try {
        fresh = await requestToken()
      } catch {
        fresh = null
      }
      if (!fresh) {
        onError(TOKEN_ERROR)
        return
      }
      try {
        response = await open(fresh)
      } catch {
        if (!controller.signal.aborted) onError(CONNECT_ERROR)
        return
      }
      if (response.status === 401) {
        await cancelBody(response)
        onError(TOKEN_ERROR)
        return
      }
    }
    if (!response.ok) {
      await cancelBody(response)
      onError(`Gagal membuka stream transkrip (${response.status}).`)
      return
    }
    await readStream(response.body)
  }
}

// cancelBody closes a body that is not read, so the connection of the server does not stay open.
async function cancelBody(response) {
  try {
    await response?.body?.cancel?.()
  } catch {
    // The body is already closed: there is nothing left to cancel.
  }
}

// errorMessage reads the message of the error event. The event never carries provider detail, so
// the generic message is enough when the payload cannot be read.
function errorMessage(data) {
  try {
    const parsed = JSON.parse(data)
    if (parsed?.message) return parsed.message
  } catch {
    // The payload is not JSON: the generic message is reported instead.
  }
  return TRANSCRIPT_ERROR
}
