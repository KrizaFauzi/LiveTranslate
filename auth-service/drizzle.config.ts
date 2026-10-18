import { defineConfig } from 'drizzle-kit';
// Explicitly load root .env with Node --env-file when running migration commands.
export default defineConfig({
  dialect: 'postgresql', schema: './src/db/schema.ts', out: '../migration',
  schemaFilter: ['public'],
  ...(process.env.MIGRATION_DATABASE_URL ? { dbCredentials: { url: process.env.MIGRATION_DATABASE_URL } } : {}),
  migrations: { schema: 'drizzle', table: '__drizzle_migrations' },
});
