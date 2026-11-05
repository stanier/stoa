// Pure audio helpers for voice mode. No browser APIs here, so they run
// under `node --test` too.
//
// Wire format (see internal/realtime): mono 16-bit little-endian PCM,
// base64 encoded, at the rates the server announces in its "ready" event.

// Resample mono Float32 audio with linear interpolation. Good enough for
// speech; the browser's mic rate (usually 44.1 or 48 kHz) rarely matches the
// model's (often 16 or 24 kHz), and llama.cpp does not resample for us.
export function resample(input, fromRate, toRate) {
  if (fromRate === toRate || input.length === 0) return Float32Array.from(input)
  const ratio = fromRate / toRate
  const outLen = Math.max(1, Math.round(input.length / ratio))
  const out = new Float32Array(outLen)
  for (let i = 0; i < outLen; i++) {
    const pos = i * ratio
    const i0 = Math.floor(pos)
    const i1 = Math.min(i0 + 1, input.length - 1)
    const frac = pos - i0
    out[i] = input[Math.min(i0, input.length - 1)] * (1 - frac) + input[i1] * frac
  }
  return out
}

// Float32 [-1, 1] → Int16 PCM, clamped.
export function floatToInt16(input) {
  const out = new Int16Array(input.length)
  for (let i = 0; i < input.length; i++) {
    const s = Math.max(-1, Math.min(1, input[i]))
    out[i] = s < 0 ? Math.round(s * 0x8000) : Math.round(s * 0x7fff)
  }
  return out
}

// Int16 PCM → Float32 [-1, 1].
export function int16ToFloat(input) {
  const out = new Float32Array(input.length)
  for (let i = 0; i < input.length; i++) out[i] = input[i] / 0x8000
  return out
}

function bytesToBase64(bytes) {
  if (typeof Buffer !== 'undefined') return Buffer.from(bytes).toString('base64')
  let bin = ''
  const CHUNK = 0x8000
  for (let i = 0; i < bytes.length; i += CHUNK) {
    bin += String.fromCharCode.apply(null, bytes.subarray(i, i + CHUNK))
  }
  return btoa(bin)
}

function base64ToBytes(b64) {
  if (typeof Buffer !== 'undefined') return new Uint8Array(Buffer.from(b64, 'base64'))
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

// Int16Array → base64 of its little-endian bytes.
export function int16ToBase64(pcm) {
  const bytes = new Uint8Array(pcm.length * 2)
  const view = new DataView(bytes.buffer)
  for (let i = 0; i < pcm.length; i++) view.setInt16(i * 2, pcm[i], true)
  return bytesToBase64(bytes)
}

// base64 little-endian PCM16 → Int16Array. A trailing odd byte is dropped.
export function base64ToInt16(b64) {
  const bytes = base64ToBytes(b64)
  const n = bytes.length >> 1
  const view = new DataView(bytes.buffer, bytes.byteOffset, n * 2)
  const out = new Int16Array(n)
  for (let i = 0; i < n; i++) out[i] = view.getInt16(i * 2, true)
  return out
}

// Root-mean-square level of a Float32 frame, 0..1.
export function rms(frame) {
  if (!frame.length) return 0
  let sum = 0
  for (let i = 0; i < frame.length; i++) sum += frame[i] * frame[i]
  return Math.sqrt(sum / frame.length)
}

// EnergyVAD is a tiny voice-activity detector for hands-free turns: speech
// starts when the level stays above `threshold` for `attackMs`, and the turn
// ends after `hangMs` below it. llama.cpp's realtime endpoint has no
// confirmed server-side VAD, so the browser decides when a turn ends.
export class EnergyVAD {
  constructor({ threshold = 0.02, attackMs = 120, hangMs = 800 } = {}) {
    this.threshold = threshold
    this.attackMs = attackMs
    this.hangMs = hangMs
    this.speaking = false
    this.aboveSince = null
    this.belowSince = null
  }

  // push a frame's level at time `now` (ms). Returns 'start', 'end' or null.
  push(level, now) {
    if (level >= this.threshold) {
      this.belowSince = null
      if (this.aboveSince === null) this.aboveSince = now
      if (!this.speaking && now - this.aboveSince >= this.attackMs) {
        this.speaking = true
        return 'start'
      }
    } else {
      this.aboveSince = null
      if (this.speaking) {
        if (this.belowSince === null) this.belowSince = now
        if (now - this.belowSince >= this.hangMs) {
          this.speaking = false
          this.belowSince = null
          return 'end'
        }
      }
    }
    return null
  }

  reset() {
    this.speaking = false
    this.aboveSince = null
    this.belowSince = null
  }
}
