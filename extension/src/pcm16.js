export function toMonoPcm16(channels, length) {
  const out = new Int16Array(length)
  for (let i = 0; i < length; i += 1) {
    let sum = 0
    for (const channel of channels) sum += channel[i] ?? 0
    const value = Math.round((sum / channels.length) * 32767)
    out[i] = value > 32767 ? 32767 : value < -32768 ? -32768 : value
  }
  return out
}

export function frameSizeFor(sampleRate, milliseconds) {
  return Math.round((sampleRate * milliseconds) / 1000)
}
