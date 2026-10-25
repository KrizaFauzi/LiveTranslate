import test from 'node:test'
import assert from 'node:assert/strict'
import { createSavedTranscripts, createTranscriptClient } from './saved-transcripts.js'

const owner = '11111111-2222-4333-8444-555555555555'
const meeting = 'aaaaaaaa-2222-4333-8444-555555555555'
const runId = 'bbbbbbbb-2222-4333-8444-555555555555'
const run = { id: runId, meeting_id: meeting, started_at: '2026-10-23T00:00:00Z', ended_at: null, save_transcript: true, transcript_state: 'saved', transcript_expires_at: '2026-11-22T00:00:00Z' }
const segment = seq => ({ seq, text: `final ${seq}`, start: seq / 2, duration: 0.5, language: 'id', user_id: owner })
const page = (segments = [], through_seq = 0, next_after_seq = null, state = 'saved') => ({ run, storage: { state, expires_at: run.transcript_expires_at, complete: state === 'saved' }, segments, through_seq, next_after_seq })
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }

test('export reads every frozen page beyond the bounded live cache and projects safe schema', async () => {
  const requests = [], downloads = []
  const controller = createSavedTranscripts({ request: async (type, input) => {
    requests.push([type, input])
    const after = input.after_seq ?? 0
    const segments = Array.from({ length: Math.min(100, 251 - after) }, (_, i) => segment(after + i + 1))
    return page(segments, 251, after + segments.length < 251 ? after + segments.length : null, 'saving')
  }, download: value => downloads.push(value) })
  controller.setContext(owner, meeting)
  controller.select(runId)
  await controller.exportJSON()
  assert.equal(downloads.length, 1)
  assert.equal(downloads[0].segments.length, 251)
  assert.equal(downloads[0].segments.at(-1).seq, 251)
  assert.deepEqual(downloads[0].storage, { state: 'saving', expires_at: '2026-11-22T00:00:00Z', complete: false, through_seq: 251 })
  assert.equal(downloads[0].schema_version, 1)
  assert.equal(downloads[0].segments[0].user_id, undefined)
  assert.deepEqual(requests.map(([, input]) => input.through_seq), [undefined, 251, 251])
})

test('an empty frozen snapshot exports through_seq zero without guessing it is missing', async () => {
  const downloads = []
  const controller = createSavedTranscripts({ request: async () => page(), download: value => downloads.push(value) })
  controller.setContext(owner, meeting); controller.select(runId)
  await controller.exportJSON()
  assert.equal(downloads[0].storage.through_seq, 0)
  assert.deepEqual(downloads[0].segments, [])
})

for (const change of ['owner', 'meeting', 'selection', 'delete']) {
  test(`${change} changes invalidate pending reads and exports before text or download publication`, async () => {
    const waiting = deferred(), downloads = []
    const controller = createSavedTranscripts({ request: async type => type === 'transcript.run.delete' ? { run, storage: { state: 'deleted', complete: false } } : waiting.promise, download: value => downloads.push(value) })
    controller.setContext(owner, meeting); controller.select(runId)
    const read = controller.read(), exporting = controller.exportJSON()
    if (change === 'owner') controller.setContext('other-owner', meeting)
    if (change === 'meeting') controller.setContext(owner, 'other-meeting')
    if (change === 'selection') controller.select('other-run')
    if (change === 'delete') await controller.deleteSelected()
    waiting.resolve(page([segment(1)], 1))
    await Promise.all([read, exporting])
    assert.deepEqual(controller.getState().segments, [])
    assert.deepEqual(downloads, [])
  })
}

for (const state of ['deleted', 'expired', 'disabled']) {
  test(`${state} encountered mid-export discards all gathered text`, async () => {
    const downloads = []; let count = 0
    const controller = createSavedTranscripts({ request: async () => ++count === 1 ? page([segment(1)], 3, 1) : page([], 3, null, state), download: value => downloads.push(value) })
    controller.setContext(owner, meeting); controller.select(runId)
    await controller.exportJSON()
    assert.deepEqual(downloads, [])
  })
}

test('failed or truncated pagination never downloads a partial successful JSON', async () => {
  for (const failure of ['network', 'truncated']) {
    const downloads = []; let count = 0
    const controller = createSavedTranscripts({ request: async () => {
      if (++count === 1) return page([segment(1)], 9, 1)
      if (failure === 'network') throw new Error('offline')
      return page([segment(3)], 9)
    }, download: value => downloads.push(value) })
    controller.setContext(owner, meeting); controller.select(runId)
    await controller.exportJSON()
    assert.deepEqual(downloads, [], failure)
    assert.ok(controller.getState().error)
  }
})

test('saved lists and timeline paginate independently from live selection', async () => {
  const requests = []
  const controller = createSavedTranscripts({ request: async (type, input) => {
    requests.push([type, input])
    if (type === 'transcript.runs.list') return input.cursor ? { runs: [{ ...run, id: 'second' }], next_cursor: null } : { runs: [run], next_cursor: 'next' }
    return input.after_seq ? page([segment(7)], 7) : page([segment(2)], 7, 2)
  } })
  controller.setContext(owner, meeting)
  await controller.list(); await controller.list(true)
  assert.equal(controller.getState().runs.length, 2)
  controller.select(runId); await controller.read(); await controller.read(true)
  assert.deepEqual(controller.getState().segments.map(value => value.seq), [2, 7])
  assert.equal(requests.at(-1)[1].through_seq, 7)
  controller.select(null)
  assert.deepEqual(controller.getState().segments, [])
  assert.equal(controller.getState().runs.length, 2)
})

test('worker refresh stays same-account and never returns tokens to the popup', async () => {
  let context = { ownerId: owner, meetingId: meeting, generation: 1 }, calls = 0
  const client = createTranscriptClient({ context: async () => context, credentials: async ({ force } = {}) => ({ userId: owner, accessToken: force ? 'new-secret' : 'old-secret' }), fetchImpl: async (url, options) => {
    assert.equal(new URL(url).searchParams.get('through_seq'), '0')
    calls += 1
    if (calls === 1) return Response.json({}, { status: 401 })
    assert.equal(options.headers.Authorization, 'Bearer new-secret')
    return Response.json(page())
  }, baseUrl: 'https://api.test' })
  const result = await client.read({ meeting_id: meeting, run_id: runId, through_seq: 0 })
  assert.equal(calls, 2)
  assert.equal(result.through_seq, 0)
  assert.ok(!JSON.stringify(result).includes('secret'))
  const waiting = deferred()
  const stale = createTranscriptClient({ context: async () => context, credentials: async () => ({ userId: owner, accessToken: 'secret' }), fetchImpl: async () => waiting.promise, baseUrl: 'https://api.test' })
  const read = stale.read({ meeting_id: meeting, run_id: runId })
  await new Promise(resolve => setImmediate(resolve))
  context = { ...context, ownerId: 'other', generation: 2 }
  waiting.resolve(Response.json(page([segment(1)], 1)))
  await assert.rejects(read, /Sesi|berubah/)
})

test('worker rejects unrelated meetings and an account switch during refresh', async () => {
  let currentOwner = owner, fetches = 0
  const client = createTranscriptClient({ context: async () => ({ ownerId: currentOwner, meetingId: meeting, generation: 1 }), credentials: async ({ force } = {}) => {
    if (force) currentOwner = 'other'
    return { userId: currentOwner, accessToken: 'private' }
  }, fetchImpl: async () => { fetches += 1; return Response.json({}, { status: 401 }) }, baseUrl: 'https://api.test' })
  await assert.rejects(client.list({ meeting_id: 'cccccccc-2222-4333-8444-555555555555' }))
  assert.equal(fetches, 0)
  await assert.rejects(client.read({ meeting_id: meeting, run_id: runId }))
  assert.equal(fetches, 1, 'another account never retries the old run request')
})

test('an export started during saving remains an incomplete snapshot if the writer finishes between pages', async () => {
  const downloads = []; let count = 0
  const controller = createSavedTranscripts({ request: async () => ++count === 1 ? page([segment(1)], 2, 1, 'saving') : page([segment(2)], 2), download: value => downloads.push(value) })
  controller.setContext(owner, meeting); controller.select(runId)
  await controller.exportJSON()
  assert.equal(downloads[0].storage.complete, false)
})

test('returning to the same owner and meeting does not revive an earlier pending list or export', async () => {
  const waiting = deferred(), downloads = []
  const controller = createSavedTranscripts({ request: async type => {
    await waiting.promise
    return type === 'transcript.runs.list' ? { runs: [run], next_cursor: null } : page([segment(1)], 1)
  }, download: value => downloads.push(value) })
  controller.setContext(owner, meeting); controller.select(runId)
  const listing = controller.list(), exporting = controller.exportJSON()
  controller.setContext('other-owner', 'other-meeting')
  controller.setContext(owner, meeting); controller.select(runId)
  waiting.resolve()
  await Promise.all([listing, exporting])
  assert.deepEqual(controller.getState().runs, [])
  assert.deepEqual(controller.getState().segments, [])
  assert.deepEqual(downloads, [])
})

test('a changed snapshot ceiling cannot produce a successful export', async () => {
  const downloads = []; let count = 0
  const controller = createSavedTranscripts({ request: async () => ++count === 1 ? page([segment(1)], 2, 1) : page([segment(2)], 3, 2), download: value => downloads.push(value) })
  controller.setContext(owner, meeting); controller.select(runId)
  await controller.exportJSON()
  assert.deepEqual(downloads, [])
  assert.ok(controller.getState().error)
})

test('a pending deletion survives Live and same-run reselection and fences pre-delete reads', async () => {
  const deleting = deferred(), reading = deferred()
  const controller = createSavedTranscripts({ request: async type => {
    if (type === 'transcript.runs.list') return { runs: [run], next_cursor: null }
    return type === 'transcript.run.delete' ? deleting.promise : reading.promise
  } })
  controller.setContext(owner, meeting); await controller.list(); controller.select(runId)
  const removal = controller.deleteSelected()
  controller.select(null); controller.select(runId)
  const staleRead = controller.read()
  deleting.resolve({ run, storage: { state: 'deleted', complete: false } })
  await removal
  reading.resolve(page([segment(1)], 1))
  await staleRead
  assert.deepEqual(controller.getState().segments, [])
  assert.equal(controller.getState().storage.state, 'deleted')
  assert.equal(controller.getState().deleting, false)
  assert.deepEqual(controller.getState().runs, [])
})

for (const terminal of ['deleted', 'expired']) {
  test(`timeline ${terminal} observation invalidates a concurrently pending export`, async () => {
    const exporting = deferred(), downloads = []
    const controller = createSavedTranscripts({ request: async (_type, input) => input.limit === 100 ? exporting.promise : page([], 0, null, terminal), download: value => downloads.push(value) })
    controller.setContext(owner, meeting); controller.select(runId)
    const pendingExport = controller.exportJSON()
    await controller.read()
    exporting.resolve(page([segment(1)], 1))
    await pendingExport
    assert.deepEqual(downloads, [])
    assert.equal(controller.getState().storage.state, terminal)
    assert.deepEqual(controller.getState().segments, [])
    assert.equal(controller.getState().exporting, false)
  })

  test(`export ${terminal} observation invalidates a concurrently pending timeline read`, async () => {
    const reading = deferred(), downloads = []
    const controller = createSavedTranscripts({ request: async (_type, input) => input.limit === 50 ? reading.promise : page([], 0, null, terminal), download: value => downloads.push(value) })
    controller.setContext(owner, meeting); controller.select(runId)
    const pendingRead = controller.read()
    await controller.exportJSON()
    reading.resolve(page([segment(1)], 1))
    await pendingRead
    assert.deepEqual(downloads, [])
    assert.equal(controller.getState().storage.state, terminal)
    assert.deepEqual(controller.getState().segments, [])
    assert.equal(controller.getState().loading, false)
  })
}

test('storage polling preserves the loaded timeline extent, frozen snapshot and continuation', async () => {
  let requests = 0
  const controller = createSavedTranscripts({ request: async (_type, input) => {
    requests += 1
    const after = input.after_seq ?? 0
    const ceiling = requests <= 2 ? 150 : (input.through_seq ?? 200)
    return page(Array.from({ length: 50 }, (_, i) => segment(after + i + 1)), ceiling, after + 50, requests <= 2 ? 'saving' : 'saved')
  } })
  controller.setContext(owner, meeting); controller.select(runId)
  await controller.read(); await controller.read(true)
  await controller.read(false, { preserveTimeline: true })
  assert.equal(controller.getState().segments.length, 100)
  assert.equal(controller.getState().segments.at(-1).seq, 100)
  assert.equal(controller.getState().through_seq, 150)
  assert.equal(controller.getState().next_after_seq, 100)
  assert.equal(controller.getState().storage.state, 'saved')
})

test('terminal archive observation releases a pending list refresh without restoring deleted runs', async () => {
  const listing = deferred()
  const controller = createSavedTranscripts({ request: async type => type === 'transcript.runs.list' ? listing.promise : page([], 0, null, 'deleted') })
  controller.setContext(owner, meeting); controller.select(runId)
  const pending = controller.list()
  await controller.read()
  listing.resolve({ runs: [run], next_cursor: null })
  await pending
  assert.equal(controller.getState().listing, false)
  assert.deepEqual(controller.getState().runs, [])
  assert.equal(controller.getState().storage.state, 'deleted')
})
