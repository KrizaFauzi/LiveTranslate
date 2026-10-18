import test from 'node:test';
import assert from 'node:assert/strict';
import { isAuthConfigured } from '../src/config.ts';

const base = { url: 'https://project.supabase.co', key: 'test-publishable-key' };

test('accepts HTTP redirect for localhost and loopback development', () => {
  for (const redirect of [
    'http://localhost:8989/auth/confirmed',
    'http://127.0.0.1:8989/auth/confirmed',
    'http://[::1]:8989/auth/confirmed',
  ]) assert.equal(isAuthConfigured({ ...base, redirect }), true, redirect);
});

test('accepts production HTTPS redirect', () => {
  assert.equal(isAuthConfigured({ ...base, redirect: 'https://api.example.com/auth/confirmed' }), true);
});

test('rejects public HTTP and lookalike localhost hosts', () => {
  for (const redirect of [
    'http://example.com/auth/confirmed',
    'http://localhost.example.com/auth/confirmed',
    'http://localhost@evil.example/auth/confirmed',
    'http://192.168.1.2/auth/confirmed',
    'http://0.0.0.0:8989/auth/confirmed',
    'https://user:password@example.com/auth/confirmed',
  ]) assert.equal(isAuthConfigured({ ...base, redirect }), false, redirect);
});

test('rejects missing credentials, malformed URLs and non-HTTPS Supabase', () => {
  const valid = { ...base, redirect: 'https://api.example.com/auth/confirmed' };
  for (const config of [
    { ...valid, url: '' }, { ...valid, key: '' }, { ...valid, key: '   ' },
    { ...valid, redirect: '' }, { ...valid, redirect: 'not-a-url' },
    { ...valid, url: 'http://project.supabase.co' },
    { ...valid, url: 'https://user:password@project.supabase.co' },
  ]) assert.equal(isAuthConfigured(config), false);
});
