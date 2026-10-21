import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import * as urls from './meeting-url.js'
const fixtures = JSON.parse(readFileSync(new URL('../../fixtures/meeting-urls.json', import.meta.url)))
for (const fixture of fixtures) test(`meeting identity: ${fixture.url.replace(/\?.*/, '')}`, () => {
 const value = urls.classifyMeetingURL?.(fixture.url)
 if (!fixture.key) assert.equal(value, null)
 else {
  assert.equal(value?.meeting_key, fixture.key)
  assert.equal(value?.canonical_url, fixture.canonical)
 }
})
