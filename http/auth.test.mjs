import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

// Run the response-handling code from the real .http file against HTTP fixtures.
function handle(name, status, data) {
  const section = fs.readFileSync(new URL('./auth.http', import.meta.url), 'utf8')
    .split(/^### /m).find(part => part.includes('# @name ' + name + '\n'));
  const values = new Map([["userId", "user-a"]]);
  const context = vm.createContext({
    response: { statusCode: status, json: () => data },
    vars: { get: key => values.get(key), global: { set: (key, value) => values.set(key, value) } },
  });
  for (const capture of section.matchAll(/^# @capture global-secret (\w+) = (.+)$/gm)) {
    const expression = capture[2].replace('response.json.', 'response.json().');
    new vm.Script('vars.global.set(' + JSON.stringify(capture[1]) + ', ' + expression + ')').runInContext(context);
  }
  for (const script of section.matchAll(/^# @script test\n> \{%\n([\s\S]*?)^%\}/gm)) {
    new vm.Script(script[1]).runInContext(context);
  }
  return values;
}
for (const name of ['Login', 'Refresh']) {
  test(name + ': reports backend HTTP error instead of dereferencing a missing session', () => {
    assert.throws(() => handle(name, 503, { error: { message: 'Autentikasi belum tersedia.' } }), /HTTP 503: Autentikasi belum tersedia\./);
    assert.throws(() => handle(name, 401, { error: { message: 'Sesi tidak valid.' } }), /HTTP 401: Sesi tidak valid\./);
  });
  test(name + ': rejects malformed success and non-JSON error responses', () => {
    assert.throws(() => handle(name, 200, {}), /sesi valid/);
    assert.throws(() => handle(name, 502, null), /HTTP 502/);
  });
  test(name + ': saves complete successful session', () => {
    const values = handle(name, 200, { user: { id: 'user-a' }, session: { access_token: 'access-new', refresh_token: 'refresh-new' } });
    assert.equal(values.get('accessToken'), 'access-new');
    assert.equal(values.get('refreshToken'), 'refresh-new');
    assert.equal(values.get('userId'), 'user-a');
  });
}

test('GetProfile: preserves backend error and rejects a different profile owner', () => {
  assert.throws(() => handle('GetProfile', 503, { error: { message: 'Database belum siap.' } }), /HTTP 503: Database belum siap\./);
  assert.throws(() => handle('GetProfile', 200, { user: { id: 'user-a' }, profile: { id: 'user-b' } }), /Profil tidak sesuai/);
  assert.doesNotThrow(() => handle('GetProfile', 200, { user: { id: 'user-a' }, profile: { id: 'user-a' } }));
});
