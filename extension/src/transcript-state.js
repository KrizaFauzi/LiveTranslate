// The transcript state lives in the session storage of the extension: it belongs to one run and
// never leaves the browser.

// At most 200 finals are kept, so a long meeting does not fill the storage of the browser.
const FINAL_LIMIT = 200

// emptyState starts the transcript of one run: no segment, no text, and the run waits for the
// first sound.
export function emptyState(runId) {
  return { run_id: runId, status: 'waiting', last_seq: 0, finals: [], partial: null }
}

// applySegment adds one segment to the state. A final replaces the partial that is running and
// joins the finals; a new partial replaces the previous one. The oldest finals are dropped when
// the list is full.
//
// A segment the client already has is ignored: the server replays its whole backlog when the
// client asks for it, and a repeated segment would show the same sentence twice.
export function applySegment(state, segment) {
  if (!segment || typeof segment.seq !== 'number') return state
  if (segment.seq <= state.last_seq) return state
  const next = { ...state, finals: [...state.finals], last_seq: segment.seq }
  if (segment.type === 'final') {
    next.finals.push({ seq: segment.seq, text: segment.text ?? '', start: segment.start, duration: segment.duration })
    if (next.finals.length > FINAL_LIMIT) next.finals = next.finals.slice(next.finals.length - FINAL_LIMIT)
    next.partial = null
  } else {
    next.partial = { seq: segment.seq, text: segment.text ?? '', start: segment.start, duration: segment.duration }
  }
  next.status = 'transcribing'
  return next
}

// applyStatus changes the status of the transcript without touching the text. The message only
// stays when there is one: it tells the user why the transcript failed.
export function applyStatus(state, status, message) {
  const next = { ...state, status }
  if (message) next.message = message
  else delete next.message
  return next
}

// transcriptLines is what the popup shows: the finals in order, then the partial that is still
// running. An empty text is left out.
export function transcriptLines(state) {
  if (!state) return []
  const lines = (state.finals ?? []).map(final => final.text ?? '').filter(Boolean)
  const partial = state.partial?.text ?? ''
  if (partial) lines.push(partial)
  return lines
}
