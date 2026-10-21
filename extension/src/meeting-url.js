// Provider-specific identity rules. Keep in sync with Go using meeting-urls.json.
export function classifyMeetingURL(input) {
  if (typeof input !== 'string' || input.length > 4096 || input !== input.trim()) return null
  let url
  try { url = new URL(input) } catch { return null }
  if (url.protocol !== 'https:' || url.username || url.password || url.port || /[\\\x00-\x20]/.test(input)) return null
  const host = url.hostname.toLowerCase(), path = url.pathname
  let provider, providerHost = host, room, canonicalPath
  if (host === 'meet.google.com') {
    room = /^\/([a-z]{3}-[a-z]{4}-[a-z]{3})\/?$/.exec(path)?.[1]
    if (!room) return null
    provider = 'google_meet'; canonicalPath = `/${room}`
  } else if (host === 'zoom.us' || /^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+zoom\.us$/.test(host)) {
    room = /^\/wc\/([0-9]{9,11})\/(?:join|start)\/?$/.exec(path)?.[1]
      ?? /^\/wc\/(?:join|start)\/([0-9]{9,11})\/?$/.exec(path)?.[1]
    if (!room) return null
    provider = 'zoom'; providerHost = 'zoom.us'; canonicalPath = `/wc/${room}/join`
  } else if (host === 'meet.jit.si') {
    room = /^\/([A-Za-z0-9_-]{1,128})\/?$/.exec(path)?.[1]
    if (!room || /^(?:static|libs|css|images|sounds|lang|fonts|external_api|http-bind|xmpp-websocket|about|privacy|terms|login|logout|register|settings|help)$/i.test(room)) return null
    room = room.toLowerCase()
    provider = 'jitsi'; canonicalPath = `/${room}`
  } else return null
  return {
    provider, provider_host: providerHost, provider_room_id: room,
    meeting_key: `${provider}:${providerHost}:${room}`,
    canonical_url: `https://${providerHost}${canonicalPath}`,
  }
}
