import { createServer } from 'node:http';
import { Readable } from 'node:stream';
import { makeHandler } from './app.ts';
import { createProvider } from './provider.ts';
import { createRepository } from './db/client.ts';

const repository = createRepository(process.env.DATABASE_URL);
const handler = makeHandler({ internalToken: process.env.AUTH_INTERNAL_TOKEN ?? '', repository,
  provider: createProvider({ url: process.env.SUPABASE_URL, key: process.env.SUPABASE_PUBLISHABLE_KEY, redirect: process.env.SUPABASE_AUTH_REDIRECT_URL }) });
const server = createServer(async (req, res) => {
  try {
    const headers = new Headers();
    for (const [name, value] of Object.entries(req.headers)) if (value) headers.set(name, Array.isArray(value) ? value.join(', ') : value);
    const hasBody = req.method !== 'GET' && req.method !== 'HEAD';
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 10000);
    req.on('aborted', () => controller.abort());
    const request = new Request('http://auth-service' + (req.url ?? '/'), {
      method: req.method, headers, body: hasBody ? Readable.toWeb(req) as ReadableStream<Uint8Array> : undefined,
      signal: controller.signal, ...(hasBody ? { duplex: 'half' } : {}),
    } as RequestInit);
    try {
      const response = await Promise.race([handler(request), new Promise<Response>(resolve => {
        controller.signal.addEventListener('abort', () => resolve(Response.json({ error: { code: 'timeout', message: 'Layanan tidak merespons.' } }, { status: 504, headers: { 'Cache-Control': 'no-store' } })), { once: true });
      })]);
      res.writeHead(response.status, Object.fromEntries(response.headers));
      res.end(Buffer.from(await response.arrayBuffer()));
    } finally { clearTimeout(timer); }
  } catch {
    res.writeHead(400, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
    res.end(JSON.stringify({ error: { code: 'invalid_request', message: 'Permintaan tidak valid.' } }));
  }
});
server.requestTimeout = 10000;
server.headersTimeout = 10000;
server.setTimeout(10000);
server.listen(3001, '0.0.0.0', () => console.log('LiveTranslate auth mendengarkan :3001'));
async function shutdown() { server.close(); await repository?.close(); }
process.on('SIGTERM', shutdown);
process.on('SIGINT', shutdown);
