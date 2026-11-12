// AudioWorklet that forwards mono mic frames to the main thread in blocks of
// ~2048 samples (about 43 ms at 48 kHz), so the page can resample, measure
// the level and send ~100 ms chunks. Loaded by VoiceSession via
// audioWorklet.addModule; served same-origin, as the CSP requires.
class CaptureProcessor extends AudioWorkletProcessor {
  constructor() {
    super()
    this.buf = new Float32Array(2048)
    this.len = 0
  }

  process(inputs) {
    const ch = inputs[0]?.[0]
    if (ch) {
      let i = 0
      while (i < ch.length) {
        const n = Math.min(ch.length - i, this.buf.length - this.len)
        this.buf.set(ch.subarray(i, i + n), this.len)
        this.len += n
        i += n
        if (this.len === this.buf.length) {
          this.port.postMessage(this.buf.slice(0))
          this.len = 0
        }
      }
    }
    return true
  }
}

registerProcessor('stoa-capture', CaptureProcessor)
