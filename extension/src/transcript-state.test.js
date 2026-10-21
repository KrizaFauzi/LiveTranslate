import test from 'node:test'
import assert from 'node:assert/strict'
import { emptyState, applySegment, applyStatus, transcriptLines } from './transcript-state.js'

test('final replaces the trailing partial and is appended once', () => {
  let state = emptyState('run-1')
  state = applySegment(state, { seq: 1, type: 'partial', text: 'sela' })
  assert.equal(state.partial.text, 'sela')
  state = applySegment(state, { seq: 2, type: 'partial', text: 'selamat' })
  assert.equal(state.partial.text, 'selamat')
  assert.equal(state.finals.length, 0)
  state = applySegment(state, { seq: 3, type: 'final', text: 'selamat pagi' })
  assert.equal(state.partial, null)
  assert.equal(state.finals.length, 1)
  assert.equal(state.finals[0].text, 'selamat pagi')
  assert.equal(state.last_seq, 3)
})

test('finals are capped and oldest entries are dropped', () => {
  let state = emptyState('run-1')
  for (let i = 1; i <= 205; i += 1) state = applySegment(state, { seq: i, type: 'final', text: `baris ${i}` })
  assert.equal(state.finals.length, 200)
  assert.equal(state.finals[0].text, 'baris 6')
})

test('state is scoped to one run', () => {
  const state = emptyState('run-2')
  assert.equal(state.run_id, 'run-2')
  assert.equal(state.finals.length, 0)
  assert.equal(state.partial, null)
})

test('a partial only fills the partial and advances the sequence number', () => {
  let state = emptyState('run-1')
  state = applySegment(state, { seq: 7, type: 'partial', text: 'selamat pagi' })
  assert.equal(state.partial.text, 'selamat pagi')
  assert.equal(state.finals.length, 0)
  assert.equal(state.last_seq, 7)
  assert.equal(state.status, 'transcribing')
})

test('a segment the client already has is ignored', () => {
  let state = emptyState('run-1')
  state = applySegment(state, { seq: 4, type: 'final', text: 'satu' })
  state = applySegment(state, { seq: 4, type: 'final', text: 'satu' })
  assert.equal(state.finals.length, 1, 'the replayed segment is not added twice')
  assert.equal(state.last_seq, 4)
  state = applySegment(state, { seq: 3, type: 'final', text: 'lama' })
  assert.equal(state.finals.length, 1, 'an older segment is ignored')
  assert.equal(state.last_seq, 4)
})

test('the status changes without touching the text', () => {
  const state = emptyState('run-1')
  const failed = applyStatus(state, 'transcript_error', 'Transkripsi gagal.')
  assert.equal(failed.status, 'transcript_error')
  assert.equal(failed.message, 'Transkripsi gagal.')
  assert.equal(failed.finals.length, 0)
  assert.equal(state.status, 'waiting', 'the state of the caller is not changed')
})

test('the popup shows the finals in order and the partial that is still running', () => {
  let state = emptyState('run-1')
  state = applySegment(state, { seq: 1, type: 'final', text: 'satu' })
  state = applySegment(state, { seq: 2, type: 'partial', text: 'dua' })
  assert.deepEqual(transcriptLines(state), ['satu', 'dua'])
  state = applySegment(state, { seq: 3, type: 'final', text: 'tiga' })
  assert.deepEqual(transcriptLines(state), ['satu', 'tiga'], 'the final replaced the partial')
})

test('an empty text is left out of the popup', () => {
  let state = emptyState('run-1')
  state = applySegment(state, { seq: 1, type: 'final', text: '' })
  state = applySegment(state, { seq: 2, type: 'partial', text: '' })
  assert.deepEqual(transcriptLines(state), [])
})
