import test from 'node:test';
import assert from 'node:assert/strict';
import { makeHandler } from '../src/app.ts';

const id = '11111111-1111-4111-8111-111111111111';
const session = { access_token: 'access', refresh_token: 'refresh', expires_at: 2000000000, token_type: 'bearer', secret: 'hidden' };
const user = { id, email: 'a@example.com', raw_user_meta_data: { secret: 'hidden' } };
function fixture(overrides = {}) {
  return makeHandler({
    internalToken: 'internal-secret',
    provider: {
      register: async () => ({ user, session: null }),
      login: async () => ({ user, session }),
      refresh: async () => ({ user, session }),
      getUser: async (token: string) => { assert.equal(token, 'access'); return user; },
      logout: async () => {},
    },
    repository: {
      profile: async (userId: string) => {
        assert.equal(userId, id);
        return { id, display_name: 'A', created_at: '2026-01-01', updated_at: '2026-01-01' };
      },
      check: async () => {},
    },
    ...overrides,
  });
}
function request(path: string, body?: unknown, options: RequestInit = {}) {
  return new Request('http://internal' + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'X-Internal-Token': 'internal-secret', 'Content-Type': 'application/json', Authorization: 'Bearer access' },
    body: body === undefined ? undefined : JSON.stringify(body),
    ...options,
  });
}
test('requires internal secret; health is public', async () => {
  const handler = fixture();
  assert.equal((await handler(request('/api/auth/me', undefined, { headers: {} }))).status, 401);
  assert.equal((await handler(request('/health', undefined, { headers: {} }))).status, 200);
});
test('register returns confirmation without session', async () => {
  const response = await fixture()(request('/api/auth/register', { display_name: 'A', email: 'a@example.com', password: 'password1' }));
  assert.equal(response.status, 202);
  assert.equal((await response.json()).session, undefined);
  assert.equal(response.headers.get('Cache-Control'), 'no-store');
});
test('login exposes only public user and session fields', async () => {
  const response = await fixture()(request('/api/auth/login', { email: 'a@example.com', password: 'password1' }));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { user: { id, email: 'a@example.com' }, session: { access_token: 'access', refresh_token: 'refresh', expires_at: 2000000000, token_type: 'bearer' } });
});
test('rejects malformed bodies and invalid credentials input', async () => {
  const handler = fixture();
  for (const body of [null, [], {}, { email: 'bad', password: 'password1' }, { email: 'a@example.com', password: 'short' }, { email: 'a@example.com', password: 'password1', user_id: id }]) {
    assert.equal((await handler(request('/api/auth/login', body))).status, 400);
  }
  assert.equal((await handler(request('/api/auth/login', {}, { headers: { 'X-Internal-Token': 'internal-secret', 'Content-Type': 'text/plain' } }))).status, 415);
  assert.equal((await handler(request('/api/auth/login', { email: 'a'.repeat(17000) }))).status, 413);
});
test('profile query uses verified user identity', async () => {
  const response = await fixture()(request('/api/auth/me'));
  assert.equal(response.status, 200);
  assert.equal((await response.json()).profile.id, id);
  assert.equal((await fixture()(request('/api/auth/me', undefined, { headers: { 'X-Internal-Token': 'internal-secret' } }))).status, 401);
});
test('maps provider errors without leaking details', async () => {
  const handler = fixture({ provider: { login: async () => { throw { status: 400, message: 'SQL private details' }; } } });
  const response = await handler(request('/api/auth/login', { email: 'a@example.com', password: 'password1' }));
  assert.equal(response.status, 401);
  assert.ok(!(await response.text()).includes('SQL'));
});
test('refresh projects new session and logout has empty body', async () => {
  const handler = fixture();
  assert.equal((await handler(request('/api/auth/refresh', { refresh_token: 'refresh' }))).status, 200);
  const response = await handler(request('/api/auth/logout', undefined, { method: 'POST' }));
  assert.equal(response.status, 204);
  assert.equal(await response.text(), '');
});
test('unconfigured auth fails safely while health stays alive', async () => {
  const handler = fixture({ provider: null });
  assert.equal((await handler(request('/api/auth/login', { email: 'a@example.com', password: 'password1' }))).status, 503);
  assert.equal((await handler(request('/internal/database-check'))).status, 200);
  assert.equal((await handler(request('/api/auth/unknown'))).status, 404);
});
