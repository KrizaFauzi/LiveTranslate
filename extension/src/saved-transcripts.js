const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const UNAVAILABLE = new Set(['disabled', 'deleted', 'expired'])
const changedSession = () => new Error('Sesi atau pilihan meeting sudah berubah.')

// Only the worker constructs this client. Credentials never cross the popup RPC boundary.
export function createTranscriptClient({ credentials, context, fetchImpl = globalThis.fetch, baseUrl = '' }) {
  async function call(method, input, listing = false) {
    if (!UUID.test(input?.meeting_id) || (!listing && !UUID.test(input?.run_id))) throw new Error('Identitas transkrip tidak valid.')
    const initial = await context()
    if (!initial.ownerId || initial.meetingId !== input.meeting_id) throw changedSession()
    const current = async () => {
      const value = await context()
      if (value.ownerId !== initial.ownerId || value.meetingId !== initial.meetingId || value.generation !== initial.generation) throw changedSession()
    }
    let auth = await credentials()
    await current()
    if (auth?.userId !== initial.ownerId) throw changedSession()
    const query = new URLSearchParams({ meeting_id: input.meeting_id })
    for (const key of listing ? ['cursor'] : ['after_seq', 'through_seq', 'limit']) {
      if (input[key] !== undefined) query.set(key, String(input[key]))
    }
    const path = listing ? '/api/audio/runs' : `/api/audio/runs/${input.run_id}/segments`
    for (let attempt = 0; ; attempt += 1) {
      const response = await fetchImpl(`${baseUrl.replace(/\/$/, '')}${path}?${query}`, {
        method, headers: { Authorization: `Bearer ${auth.accessToken}` }, cache: 'no-store', signal: AbortSignal.timeout(15000),
      })
      await current()
      if (response.status === 401 && attempt === 0) {
        auth = await credentials({ force: true })
        await current()
        if (auth?.userId !== initial.ownerId) throw changedSession()
        continue
      }
      const data = await response.json().catch(() => null)
      await current()
      if (!response.ok) throw new Error(data?.error?.message || 'Transkrip tersimpan tidak dapat diakses.')
      if (!listing && (data?.run?.id !== input.run_id || data.run.meeting_id !== input.meeting_id)) throw changedSession()
      if (listing && (!Array.isArray(data?.runs) || data.runs.some(run => run.meeting_id !== input.meeting_id))) throw changedSession()
      return data
    }
  }
  return { list: input => call('GET', input, true), read: input => call('GET', input), delete: input => call('DELETE', input) }
}

function validatePage(page, runId, meetingId, after, through) {
  if (page?.run?.id !== runId || page.run.meeting_id !== meetingId || !page.storage) throw new Error('Respons transkrip tidak valid.')
  if (UNAVAILABLE.has(page.storage.state)) throw new Error('Transkrip dihapus, kedaluwarsa, atau tidak disimpan.')
  if (!Number.isSafeInteger(page.through_seq) || page.through_seq < 0 || (through !== undefined && page.through_seq !== through) || !Array.isArray(page.segments)) throw new Error('Snapshot transkrip tidak valid.')
  let previous = after
  for (const segment of page.segments) {
    if (!Number.isSafeInteger(segment.seq) || segment.seq <= previous || segment.seq > page.through_seq || typeof segment.text !== 'string') throw new Error('Urutan transkrip tidak valid.')
    previous = segment.seq
  }
  if (page.next_after_seq !== null) {
    if (!page.segments.length || page.next_after_seq !== previous || previous >= page.through_seq) throw new Error('Paginasi transkrip tidak lengkap.')
  } else if (previous !== page.through_seq) throw new Error('Snapshot transkrip tidak lengkap.')
  return page
}

const initialState = (ownerId, meetingId) => ({ owner_id: ownerId, meeting_id: meetingId, runs: [], next_cursor: null, selected: null, run: null, storage: null, segments: [], through_seq: undefined, next_after_seq: null, listing: false, loading: false, exporting: false, deleting: false, error: '' })

// Saved pages and export have their own lifecycle; neither reads nor truncates the live cache.
export function createSavedTranscripts({ request, onChange = () => {}, download = downloadTranscript }) {
  let state = initialState(null, null), generation = 0, contextVersion = 0, listTicket = 0, readTicket = 0, exportTicket = 0
  const pendingDeletes = new Set(), unavailableRuns = new Map()
  const publish = update => { state = { ...state, ...update }; onChange(state) }
  const valid = ticket => ticket === generation
  const input = () => ({ meeting_id: state.meeting_id, run_id: state.selected })
  const availableRuns = runs => runs.filter(run => !pendingDeletes.has(run.id) && !unavailableRuns.has(run.id))
  function observeUnavailable(runId, data) {
    unavailableRuns.set(runId, data)
    if (state.selected !== runId) {
      publish({ runs: availableRuns(state.runs) })
      return
    }
    generation += 1
    readTicket += 1
    exportTicket += 1
    publish({ run: data.run, storage: data.storage, segments: [], through_seq: data.through_seq ?? 0, next_after_seq: null, loading: false, exporting: false, deleting: pendingDeletes.has(runId), listing: false, runs: availableRuns(state.runs) })
  }
  function select(runId) {
    generation += 1
    const unavailable = unavailableRuns.get(runId)
    publish({ selected: runId || null, run: unavailable?.run ?? null, storage: unavailable?.storage ?? null, segments: [], through_seq: unavailable ? 0 : undefined, next_after_seq: null, loading: false, exporting: false, deleting: pendingDeletes.has(runId), error: '', listing: false })
  }
  async function list(more = false) {
    if (!state.owner_id || !state.meeting_id || (more && !state.next_cursor)) return
    const ticket = generation, operation = ++listTicket
    publish({ listing: true, error: '' })
    try {
      const data = await request('transcript.runs.list', { meeting_id: state.meeting_id, ...(more ? { cursor: state.next_cursor } : {}) })
      if (!valid(ticket) || operation !== listTicket) return
      const runs = more ? [...state.runs, ...data.runs.filter(run => !state.runs.some(old => old.id === run.id))] : data.runs
      publish({ runs: availableRuns(runs), next_cursor: data.next_cursor, listing: false })
    } catch (error) { if (valid(ticket) && operation === listTicket) publish({ listing: false, error: error.message }) }
  }
  async function read(more = false, { preserveTimeline = false } = {}) {
    if (!state.selected || state.deleting || unavailableRuns.has(state.selected) || (more && state.next_after_seq === null)) return
    const ticket = generation, operation = ++readTicket, selected = input()
    const preserve = preserveTimeline && state.run?.id === state.selected && state.through_seq !== undefined
    const after = more ? state.next_after_seq : 0, through = more || preserve ? state.through_seq : undefined
    publish({ loading: true, error: '' })
    try {
      const data = await request('transcript.run.read', { ...selected, after_seq: after, ...(through !== undefined ? { through_seq: through } : {}), limit: preserve ? 1 : 50 })
      if (!valid(ticket) || operation !== readTicket) return
      if (data?.run?.id !== selected.run_id || data.run.meeting_id !== selected.meeting_id) throw new Error('Respons transkrip tidak valid.')
      if (UNAVAILABLE.has(data.storage?.state)) {
        observeUnavailable(selected.run_id, data)
        return
      }
      validatePage(data, selected.run_id, selected.meeting_id, after, through)
      publish(preserve
        ? { run: data.run, storage: data.storage, loading: false }
        : { run: data.run, storage: data.storage, segments: more ? [...state.segments, ...data.segments] : data.segments, through_seq: data.through_seq, next_after_seq: data.next_after_seq, loading: false })
    } catch (error) { if (valid(ticket) && operation === readTicket) publish({ loading: false, error: error.message }) }
  }
  async function exportJSON() {
    if (!state.selected || state.deleting || unavailableRuns.has(state.selected)) return
    const ticket = generation, operation = ++exportTicket, selected = input(), segments = []
    publish({ exporting: true, error: '' })
    try {
      let after = 0, through, data, complete = true
      do {
        data = await request('transcript.run.read', { ...selected, after_seq: after, ...(through !== undefined ? { through_seq: through } : {}), limit: 100 })
        if (!valid(ticket) || operation !== exportTicket) return
        if (data?.run?.id !== selected.run_id || data.run.meeting_id !== selected.meeting_id) throw new Error('Respons transkrip tidak valid.')
        if (UNAVAILABLE.has(data.storage?.state)) {
          observeUnavailable(selected.run_id, data)
          return
        }
        validatePage(data, selected.run_id, selected.meeting_id, after, through)
        complete = complete && data.storage.state === 'saved' && data.storage.complete === true
        through = data.through_seq
        segments.push(...data.segments.map(({ seq, text, start, duration, language }) => ({ seq, text, start, duration, language })))
        after = data.next_after_seq
      } while (after !== null)
      const { id, meeting_id, started_at, ended_at } = data.run
      const exported = { schema_version: 1, run: { id, meeting_id, started_at, ended_at }, storage: { state: data.storage.state, expires_at: data.storage.expires_at, complete, through_seq: through }, segments }
      if (!valid(ticket) || operation !== exportTicket) return
      download(exported)
      publish({ exporting: false })
    } catch (error) { if (valid(ticket) && operation === exportTicket) publish({ exporting: false, error: error.message }) }
  }
  async function deleteSelected() {
    if (!state.selected || pendingDeletes.has(state.selected)) return
    const selected = input(), context = contextVersion
    pendingDeletes.add(selected.run_id)
    select(selected.run_id)
    try {
      const data = await request('transcript.run.delete', selected)
      if (context !== contextVersion) return
      if (data?.run?.id !== selected.run_id || data.run.meeting_id !== selected.meeting_id || data.storage?.state !== 'deleted') throw new Error('Penghapusan transkrip belum dikonfirmasi.')
      pendingDeletes.delete(selected.run_id)
      observeUnavailable(selected.run_id, data)
    } catch (error) {
      if (context !== contextVersion) return
      pendingDeletes.delete(selected.run_id)
      if (state.selected === selected.run_id) publish({ deleting: false, error: error.message })
    }
  }
  return {
    getState: () => state,
    setContext(ownerId, meetingId) {
      if (ownerId === state.owner_id && meetingId === state.meeting_id) return
      generation += 1
      contextVersion += 1
      pendingDeletes.clear()
      unavailableRuns.clear()
      state = initialState(ownerId, meetingId); onChange(state)
    },
    select, list, read, exportJSON, deleteSelected,
  }
}

export function downloadTranscript(value) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(value, null, 2)], { type: 'application/json;charset=utf-8' }))
  const anchor = document.createElement('a')
  anchor.href = url; anchor.download = `transcript-${value.run.id}.json`
  document.body.append(anchor)
  try { anchor.click() } finally { anchor.remove(); setTimeout(() => URL.revokeObjectURL(url), 0) }
}
