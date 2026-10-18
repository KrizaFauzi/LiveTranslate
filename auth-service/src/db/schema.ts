import { sql } from 'drizzle-orm';
import { pgTable, uuid, varchar, timestamp, check, pgPolicy } from 'drizzle-orm/pg-core';
import { authUsers, authenticatedRole } from 'drizzle-orm/supabase';

export const profiles = pgTable('profiles', {
  id: uuid('id').primaryKey().references(() => authUsers.id, { onDelete: 'cascade' }),
  display_name: varchar('display_name', { length: 100 }).notNull(),
  created_at: timestamp('created_at', { withTimezone: true }).notNull().defaultNow(),
  updated_at: timestamp('updated_at', { withTimezone: true }).notNull().defaultNow(),
}, table => [
  check('profiles_display_name_check', sql`char_length(btrim(${table.display_name})) between 1 and 100`),
  pgPolicy('profiles_select_own', { for: 'select', to: authenticatedRole, using: sql`(select auth.uid()) = ${table.id}` }),
  pgPolicy('profiles_update_own', { for: 'update', to: authenticatedRole, using: sql`(select auth.uid()) = ${table.id}`, withCheck: sql`(select auth.uid()) = ${table.id}` }),
]).enableRLS();
