import { timingSafeEqual } from 'node:crypto';
export type User = { id: string; email?: string };
export type Session = { access_token: string; refresh_token: string; expires_at?: number; token_type: string };
export type AuthResult = { user: User | null; session: Session | null };
export type Profile = { id: string; display_name: string; created_at: Date | string; updated_at: Date | string };
export type Provider = {
  register(input: { display_name: string; email: string; password: string }): Promise<AuthResult>;
  login(input: { email: string; password: string }): Promise<AuthResult>;
  refresh(token: string): Promise<AuthResult>;
  getUser(token: string): Promise<User>;
  logout(token: string): Promise<void>;
};
export type Repository = { profile(id: string): Promise<Profile | undefined>; check(): Promise<void> };
export class HttpError extends Error {
  status: number; code: string;
  constructor(status: number, code: string, message: string) { super(message); this.status = status; this.code = code; }
}
function json(status: number, body?: unknown) {
  return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' } });
}
function fail(status: number, code: string, message: string): never { throw new HttpError(status, code, message); }
function authorized(received: string | null, expected: string) {
  if (!received || !expected) return false;
  const a = Buffer.from(received), b = Buffer.from(expected);
  return a.length === b.length && timingSafeEqual(a, b);
}
async function body(request: Request, fields: string[]): Promise<Record<string, string>> {
  if (request.headers.get('content-type')?.split(';')[0].trim().toLowerCase() !== 'application/json') fail(415, 'content_type', 'Gunakan JSON.');
  const reader = request.body?.getReader();
  const chunks: Uint8Array[] = []; let size = 0;
  if (reader) for (;;) {
    const { done, value } = await reader.read(); if (done) break;
    size += value.length;
    if (size > 16384) { await reader.cancel(); fail(413, 'body_limit', 'Permintaan terlalu besar.'); }
    chunks.push(value);
  }
  let input: unknown;
  try { input = JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { fail(400, 'invalid_input', 'JSON tidak valid.'); }
  if (!input || typeof input !== 'object' || Array.isArray(input)) fail(400, 'invalid_input', 'Data tidak valid.');
  const record = input as Record<string, unknown>;
  if (Object.keys(record).some(key => !fields.includes(key)) || fields.some(key => typeof record[key] !== 'string')) fail(400, 'invalid_input', 'Field tidak valid.');
  return record as Record<string, string>;
}
function credentials(input: Record<string, string>, register: boolean) {
  const email = input.email.trim(), length = [...input.password].length;
  if (email.length > 254 || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email) || length < 8 || length > 128) fail(400, 'invalid_input', 'Periksa email dan password (8–128 karakter).');
  const display_name = register ? input.display_name.trim() : '';
  if (register && ([...display_name].length < 1 || [...display_name].length > 100)) fail(400, 'invalid_input', 'Nama harus 1–100 karakter.');
  return { email, password: input.password, display_name };
}
function bearer(request: Request) {
  const match = /^Bearer ([^\s]+)$/i.exec(request.headers.get('authorization') ?? '');
  if (!match || match[1].length > 12000) fail(401, 'unauthorized', 'Silakan masuk kembali.');
  return match[1];
}
function result(data: AuthResult) {
  if (!data.user || !data.session || !data.session.access_token || !data.session.refresh_token || !data.session.expires_at) fail(502, 'provider_error', 'Respons autentikasi tidak valid.');
  const { id, email } = data.user;
  const { access_token, refresh_token, expires_at, token_type } = data.session;
  return { user: { id, email }, session: { access_token, refresh_token, expires_at, token_type } };
}
export function makeHandler(deps: { internalToken: string; provider: Provider | null; repository: Repository | null }) {
  return async (request: Request): Promise<Response> => {
    const path = new URL(request.url).pathname;
    try {
      if (path === '/health' && request.method === 'GET') return json(200, { status: 'ok', service: 'livetranslate-auth' });
      if (!deps.internalToken) fail(503, 'unavailable', 'Autentikasi belum dikonfigurasi.');
      if (!authorized(request.headers.get('x-internal-token'), deps.internalToken)) fail(401, 'unauthorized', 'Akses ditolak.');
      if (path === '/internal/database-check' && request.method === 'GET') {
        if (!deps.repository) fail(503, 'unavailable', 'Database belum dikonfigurasi.');
        await deps.repository.check(); return json(200, { status: 'ok' });
      }
      const routes: Record<string, string> = { '/api/auth/register': 'POST', '/api/auth/login': 'POST', '/api/auth/refresh': 'POST', '/api/auth/logout': 'POST', '/api/auth/me': 'GET' };
      if (!routes[path]) return json(404, { error: { code: 'not_found', message: 'Endpoint tidak ditemukan.' } });
      if (routes[path] !== request.method) return json(405, { error: { code: 'method', message: 'Metode tidak diizinkan.' } });
      const auth = deps.provider;
      if (!auth) fail(503, 'unavailable', 'Autentikasi belum dikonfigurasi.');
      if (path === '/api/auth/register') {
        const data = await auth.register(credentials(await body(request, ['display_name', 'email', 'password']), true));
        if (!data.session) return json(202, { message: 'Periksa email untuk menyelesaikan pendaftaran, lalu masuk melalui extension.' });
        return json(201, result(data));
      }
      if (path === '/api/auth/login') return json(200, result(await auth.login(credentials(await body(request, ['email', 'password']), false))));
      if (path === '/api/auth/refresh') {
        const input = await body(request, ['refresh_token']);
        if (!input.refresh_token.trim()) fail(400, 'invalid_input', 'Token tidak valid.');
        return json(200, result(await auth.refresh(input.refresh_token)));
      }
      const token = bearer(request), user = await auth.getUser(token);
      if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(user.id)) fail(401, 'unauthorized', 'Silakan masuk kembali.');
      if (path === '/api/auth/logout') { await auth.logout(token); return json(204); }
      if (!deps.repository) fail(503, 'unavailable', 'Database belum dikonfigurasi.');
      const profile = await deps.repository.profile(user.id);
      if (!profile) fail(503, 'profile_unavailable', 'Profil belum tersedia.');
      return json(200, { user: { id: user.id, email: user.email }, profile });
    } catch (error: unknown) {
      if (error instanceof HttpError) return json(error.status, { error: { code: error.code, message: error.message } });
      const e = error as { status?: number; name?: string; code?: string };
      if (e?.name === 'TimeoutError' || e?.name === 'AbortError') return json(504, { error: { code: 'timeout', message: 'Layanan tidak merespons.' } });
      if (e?.status === 429) return json(429, { error: { code: 'rate_limit', message: 'Terlalu banyak percobaan. Coba lagi nanti.' } });
      if (e?.status && [400, 401, 403, 422].includes(e.status)) {
        if (path === '/api/auth/register' && e.code === 'user_already_exists') return json(202, { message: 'Periksa email untuk menyelesaikan pendaftaran, lalu masuk melalui extension.' });
        if (path === '/api/auth/register') return json(400, { error: { code: 'registration_failed', message: 'Pendaftaran gagal. Periksa data dan kebijakan password.' } });
        return json(401, { error: { code: 'unauthorized', message: 'Kredensial atau sesi tidak valid. Periksa email konfirmasi jika diperlukan.' } });
      }
      return json(502, { error: { code: 'provider_error', message: 'Layanan sementara tidak tersedia.' } });
    }
  };
}
