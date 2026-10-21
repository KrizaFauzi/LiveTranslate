import test from 'node:test'
import assert from 'node:assert/strict'
import { toMonoPcm16, frameSizeFor } from './pcm16.js'

test('mono mixdown averages stereo frames', () => {
  const left = Float32Array.from([0.5, -0.5, 1, -1])
  const right = Float32Array.from([0.5, 0.5, 1, 1])
  const mono = toMonoPcm16([left, right], left.length)
  assert.equal(mono.length, 4)
  assert.equal(mono[0], 0x4000)
  assert.equal(mono[1], 0)
  assert.equal(mono[2], 0x7fff)
  assert.equal(mono[3], 0)
})

test('frame size follows the sample rate', () => {
  assert.equal(frameSizeFor(48000, 100), 4800)
  assert.equal(frameSizeFor(16000, 100), 1600)
})
