import { createClient } from '@supabase/supabase-js';
import type { Provider } from './app.ts';
import { isAuthConfigured, type AuthConfig } from './config.ts';

export function createProvider(config: AuthConfig): Provider | null {
  if (!isAuthConfigured(config)) return null;
  // A fresh client per operation prevents SDK in-memory sessions leaking across requests.
  const client = () => createClient(config.url!, config.key!, {
    auth: { persistSession: false, autoRefreshToken: false, detectSessionInUrl: false },
    global: { fetch: (input, init) => fetch(input, { ...init, signal: AbortSignal.timeout(10000) }) },
  });
  return {
    async register(input) {
      const { data, error } = await client().auth.signUp({ email: input.email, password: input.password,
        options: { data: { display_name: input.display_name }, emailRedirectTo: config.redirect } });
      if (error) throw error; return data;
    },
    async login(input) {
      const { data, error } = await client().auth.signInWithPassword({ email: input.email, password: input.password });
      if (error) throw error; return data;
    },
    async refresh(refresh_token) {
      const { data, error } = await client().auth.refreshSession({ refresh_token });
      if (error) throw error; return data;
    },
    async getUser(token) {
      const { data, error } = await client().auth.getUser(token);
      if (error) throw error;
      if (!data.user) throw { status: 401 };
      return data.user;
    },
    async logout(token) {
      // This method sends the user's bearer token; no service-role key is needed.
      const { error } = await client().auth.admin.signOut(token, 'local');
      if (error) throw error;
    },
  };
}
