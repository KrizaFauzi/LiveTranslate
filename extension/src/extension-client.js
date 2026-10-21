import { AuthError, createAuthClient } from './auth.js'

export function extensionRequest(type, input) {
  return globalThis.chrome.runtime.sendMessage({ type, input }).then(response => {
    if (!response?.ok) throw new AuthError(response?.error?.message || 'Background extension tidak tersedia. Muat ulang extension.', response?.error?.status)
    return response.data
  })
}
// Browser development keeps the existing direct client; extension auth has a single worker owner.
export function createPopupAuth(baseUrl) {
  if (!globalThis.chrome?.runtime?.id) return createAuthClient({ baseUrl })
  return Object.fromEntries(['restore', 'login', 'register', 'logout'].map(name => [name, input => extensionRequest(`auth.${name}`, input)]))
}
