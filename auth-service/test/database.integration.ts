import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';

// Only point these variables at a disposable Supabase project, never production.
const enabled = process.env.TEST_ALLOW_MIGRATION === 'yes' && !!process.env.TEST_DATABASE_URL && !!process.env.TEST_RUNTIME_DATABASE_URL;
test('migrations, signup/backfill, runtime role and per-user RLS on disposable Supabase', { skip: !enabled }, async () => {
  const { default: postgres } = await import('postgres');
  const { drizzle } = await import('drizzle-orm/postgres-js');
  const { migrate } = await import('drizzle-orm/postgres-js/migrator');
  const { createRepository } = await import('../src/db/client.ts');
  const admin = postgres(process.env.TEST_DATABASE_URL!, { ssl: { rejectUnauthorized: true }, prepare: false, max: 1, onnotice: () => {} });
  const ids = [randomUUID(), randomUUID(), randomUUID()];
  let repository: ReturnType<typeof createRepository> = null;
  try {
    const existing = await admin`select to_regclass('public.profiles') as name`;
    assert.equal(existing[0].name, null, 'Use a disposable Supabase project without profiles; this test applies the initial migration.');
    // Account predating the migration exercises backfill.
    await admin`insert into auth.users(id, raw_user_meta_data) values (${ids[0]}, '{"display_name":"Legacy"}'::jsonb)`;
    await migrate(drizzle(admin), { migrationsFolder: '../migration' });
    const count = await admin`select count(*)::int as value from drizzle.__drizzle_migrations`;
    await migrate(drizzle(admin), { migrationsFolder: '../migration' });
    assert.deepEqual(await admin`select count(*)::int as value from drizzle.__drizzle_migrations`, count);
    assert.equal((await admin`select display_name from public.profiles where id=${ids[0]}`)[0].display_name, 'Legacy');
    await admin`insert into auth.users(id, raw_user_meta_data) values (${ids[1]}, '{"display_name":" Alice "}'::jsonb), (${ids[2]}, '{"display_name":123}'::jsonb)`;
    assert.equal((await admin`select display_name from public.profiles where id=${ids[1]}`)[0].display_name, 'Alice');
    assert.equal((await admin`select display_name from public.profiles where id=${ids[2]}`)[0].display_name, 'Pengguna');
    repository = createRepository(process.env.TEST_RUNTIME_DATABASE_URL);
    assert.ok(repository, 'Provision a nonprivileged livetranslate_runtime LOGIN with password before running this test.');
    await repository.check();
    assert.equal((await repository.profile(ids[1]))?.display_name, 'Alice');
    await admin.begin(async tx => {
      await tx`set local role authenticated`;
      await tx`select set_config('request.jwt.claims', ${JSON.stringify({ sub: ids[1], role: 'authenticated' })}, true), set_config('request.jwt.claim.sub', ${ids[1]}, true)`;
      const visible = await tx`select id from public.profiles`;
      assert.deepEqual(visible.map(row => row.id), [ids[1]]);
      assert.equal((await tx`update public.profiles set display_name='Wrong' where id=${ids[2]} returning id`).length, 0);
      assert.equal((await tx`update public.profiles set display_name='Updated' where id=${ids[1]} returning display_name`)[0].display_name, 'Updated');
    });
    await assert.rejects(() => admin.begin(async tx => {
      await tx`set local role authenticated`;
      await tx`select set_config('request.jwt.claim.sub', ${ids[1]}, true)`;
      await tx`update public.profiles set id=${randomUUID()} where id=${ids[1]}`;
    }), /permission denied/i);
    await assert.rejects(() => admin.begin(async tx => { await tx`set local role anon`; await tx`select * from public.profiles`; }), /permission denied/i);
    // A deliberately failing BEFORE INSERT trigger must roll signup back atomically.
    const failedId = randomUUID();
    await assert.rejects(() => admin.begin(async tx => {
      await tx.unsafe(`create function public.livetranslate_test_failure() returns trigger language plpgsql as $$ begin raise exception 'forced profile failure'; end $$`);
      await tx.unsafe('create trigger livetranslate_test_failure before insert on public.profiles for each row execute function public.livetranslate_test_failure()');
      await tx`insert into auth.users(id) values (${failedId})`;
    }), /forced profile failure/);
    assert.equal((await admin`select id from auth.users where id=${failedId}`).length, 0);
  } finally {
    await repository?.close();
    await admin`delete from auth.users where id in (${ids[0]}, ${ids[1]}, ${ids[2]})`;
    await admin.end();
  }
});
