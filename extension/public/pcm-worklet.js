// pcm-worklet.js runs in the AudioWorkletGlobalScope, so it cannot import modules.
// It only forwards the captured channels to the offscreen page in ~100 ms chunks; the mono
// mixdown and the Float32 to Int16 conversion live in the page (src/pcm16.js) so the mixing
// rule has a single, tested implementation.
const CHUNK_MILLISECONDS = 100

class PcmCaptureProcessor extends AudioWorkletProcessor {
  constructor() {
    super()
    this.chunkFrames = Math.round((sampleRate * CHUNK_MILLISECONDS) / 1000)
    this.buffers = []
    this.offset = 0
  }

  process(inputs) {
    const input = inputs[0]
    if (!input || input.length === 0 || input[0].length === 0) return true
    if (this.buffers.length !== input.length) {
      this.buffers = input.map(() => new Float32Array(this.chunkFrames))
      this.offset = 0
    }
    let read = 0
    while (read < input[0].length) {
      const count = Math.min(this.chunkFrames - this.offset, input[0].length - read)
      for (let channel = 0; channel < input.length; channel += 1) {
        this.buffers[channel].set(input[channel].subarray(read, read + count), this.offset)
      }
      this.offset += count
      read += count
      if (this.offset === this.chunkFrames) this.post()
    }
    return true
  }

  post() {
    const channels = this.buffers.map(buffer => buffer.slice(0, this.chunkFrames))
    this.port.postMessage({ type: 'frames', frames: this.chunkFrames, channels }, channels.map(channel => channel.buffer))
    this.offset = 0
  }
}

registerProcessor('pcm-capture', PcmCaptureProcessor)
