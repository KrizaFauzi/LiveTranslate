import postgres from 'postgres';
import { drizzle } from 'drizzle-orm/postgres-js';
import { eq, sql } from 'drizzle-orm';
import { profiles } from './schema.ts';
import type { Repository } from '../app.ts';

export function createRepository(connectionString?: string): (Repository & { close(): Promise<void> }) | null {
  if (!connectionString) return null;
  let url: URL;
  try {
    url = new URL(connectionString);
    if (!['postgres:', 'postgresql:'].includes(url.protocol) || decodeURIComponent(url.username).split('.')[0] !== 'livetranslate_runtime') return null;
    // Remove URL options: callers cannot override TLS verification or startup role.
    url.search = '';
  } catch { return null; }
  const client = (() => {
    try {
      return postgres(url.toString(), { ssl: { rejectUnauthorized: true }, prepare: false, max: 5,
        connect_timeout: 10, idle_timeout: 20, connection: { statement_timeout: 10000 }, onnotice: () => {} });
    } catch { return null; }
  })();
  if (!client) return null;
  const db = drizzle(client);
  async function verifyRole() {
    const rows = await db.execute(sql`select current_user as name, rolsuper, rolbypassrls, rolcreaterole, rolcreatedb, rolreplication, rolinherit,
      pg_has_role(current_user, 'authenticated', 'MEMBER') as member
      from pg_roles where rolname = current_user`);
    const row = rows[0];
    if (!row || row.name !== 'livetranslate_runtime' || row.rolsuper || row.rolbypassrls || row.rolcreaterole || row.rolcreatedb || row.rolreplication || row.rolinherit || !row.member) throw new Error('Unsafe runtime role');
    const owner = await db.execute(sql`select 1 from pg_tables where schemaname = 'public' and tablename = 'profiles' and tableowner = current_user`);
    if (owner.length) throw new Error('Runtime role must not own profiles');
  }
  return {
    async check() { await verifyRole(); await db.execute(sql`select 1`); },
    async profile(id) {
      await verifyRole();
      return db.transaction(async tx => {
        await tx.execute(sql`set local role authenticated`);
        await tx.execute(sql`select set_config('request.jwt.claims', ${JSON.stringify({ sub: id, role: 'authenticated' })}, true), set_config('request.jwt.claim.sub', ${id}, true)`);
        const rows = await tx.select().from(profiles).where(eq(profiles.id, id)).limit(1);
        return rows[0];
      });
    },
    async close() { await client.end({ timeout: 5 }); },
  };
}
