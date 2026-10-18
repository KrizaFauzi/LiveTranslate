export type AuthConfig = { url?: string; key?: string; redirect?: string };

export function isAuthConfigured(config: AuthConfig): boolean {
  try {
    if (!config.url || !config.key?.trim() || !config.redirect) return false;
    const project = new URL(config.url);
    const redirect = new URL(config.redirect);
    if (project.protocol !== 'https:' || project.username || project.password || redirect.username || redirect.password) return false;
    if (redirect.protocol === 'https:') return true;
    return redirect.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(redirect.hostname);
  } catch { return false; }
}
