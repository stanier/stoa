// VoiceSession runs one voice-to-voice conversation in the browser: it opens
// /api/realtime, captures the mic, sends PCM16 at the server's input rate,
// plays reply audio as it streams in, and reports transcripts.
//
// Turn-taking is client-side. In hands-free mode an energy VAD starts a turn
// when you speak (with a short pre-roll so the first syllable isn't lost) and
// ends it after a pause; speaking while the reply plays cancels the reply
// (barge-in). In push-to-talk mode beginTurn()/endTurn() do the same by hand.

import { resample, floatToInt16, int16ToFloat, int16ToBase64, base64ToInt16, rms, EnergyVAD } from './pcm.js'

const SEND_MS = 100        // send audio in ~100 ms chunks
const PREROLL_MS = 300     // audio kept from before speech was detected

export class VoiceSession {
  // opts: { provider, model, instructions, handsFree, on: { state, level,
  //   transcriptDelta, transcript, textDelta, responseStart, responseDone, error } }
  constructor(opts) {
    this.opts = opts
    this.on = opts.on ?? {}
    this.handsFree = opts.handsFree ?? true
    this.state = 'idle'
    this.ws = null
    this.ctx = null
    this.stream = null
    this.node = null
    this.inRate = 16000
    this.outRate = 24000
    this.capturing = false
    this.pending = []          // Int16Array chunks not yet sent
    this.pendingLen = 0
    this.preroll = []          // recent Int16Array frames before speech
    this.prerollLen = 0
    this.playhead = 0
    this.sources = new Set()
    this.responding = false
    this.vad = new EnergyVAD()
    this.closed = false
  }

  setState(s) {
    if (this.state === s) return
    this.state = s
    this.on.state?.(s)
  }

  async start() {
    this.setState('connecting')
    const q = new URLSearchParams({ provider: this.opts.provider, model: this.opts.model })
    if (this.opts.instructions) q.set('instructions', this.opts.instructions)
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${location.host}/api/realtime?${q}`)
    this.ws = ws

    const ready = new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('The voice model did not answer in time.')), 20000)
      ws.onmessage = ({ data }) => {
        let ev
        try { ev = JSON.parse(data) } catch { return }
        if (ev.type === 'ready') { clearTimeout(timer); resolve() }
        this.handle(ev)
      }
      ws.onerror = () => { clearTimeout(timer); reject(new Error('Could not open a voice session.')) }
      ws.onclose = () => { clearTimeout(timer); reject(new Error('The voice session closed.')); this.onClosed() }
    })
    await ready
    ws.onclose = () => this.onClosed()
    ws.onerror = () => this.on.error?.('Voice connection lost.')

    if (!navigator.mediaDevices?.getUserMedia) {
      throw new Error('Microphone access needs a secure (https) page.')
    }
    this.stream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    })
    this.ctx = new AudioContext()
    await this.ctx.audioWorklet.addModule(new URL('./capture-worklet.js', import.meta.url))
    const src = this.ctx.createMediaStreamSource(this.stream)
    this.node = new AudioWorkletNode(this.ctx, 'stoa-capture')
    this.node.port.onmessage = ({ data }) => this.onFrame(data)
    src.connect(this.node)
    // The worklet must be pulled by the graph to run; route it to a muted gain.
    const mute = this.ctx.createGain()
    mute.gain.value = 0
    this.node.connect(mute).connect(this.ctx.destination)
    this.setState('listening')
  }

  handle(ev) {
    switch (ev.type) {
      case 'ready':
        if (ev.input_rate) this.inRate = ev.input_rate
        if (ev.output_rate) this.outRate = ev.output_rate
        break
      case 'audio':
        this.play(ev.audio)
        break
      case 'text.delta':
        this.on.textDelta?.(ev.text)
        break
      case 'transcript.delta':
        this.on.transcriptDelta?.(ev.text)
        break
      case 'transcript.done':
        this.on.transcript?.(ev.text)
        break
      case 'response.started':
        this.responding = true
        this.on.responseStart?.()
        this.setState('thinking')
        break
      case 'response.done':
        this.responding = false
        this.on.responseDone?.()
        if (!this.sources.size && this.state !== 'user-speaking') this.setState('listening')
        break
      case 'error':
        this.on.error?.(ev.message || 'Voice error')
        if (ev.fatal) this.stop()
        break
    }
  }

  onFrame(frame) {
    if (this.closed || !this.ctx) return
    const level = rms(frame)
    this.on.level?.(level)
    const pcm = floatToInt16(resample(frame, this.ctx.sampleRate, this.inRate))

    if (this.handsFree) {
      // Raise the bar while the reply plays so echo doesn't count as speech.
      this.vad.threshold = this.sources.size ? 0.06 : 0.02
      const edge = this.vad.push(level, performance.now())
      if (edge === 'start') this.beginTurn()
      if (!this.capturing) this.keepPreroll(pcm)
      else this.queue(pcm)
      if (edge === 'end') this.endTurn()
      return
    }
    if (this.capturing) this.queue(pcm)
  }

  keepPreroll(pcm) {
    this.preroll.push(pcm)
    this.prerollLen += pcm.length
    const max = (this.inRate * PREROLL_MS) / 1000
    while (this.prerollLen - this.preroll[0].length >= max) {
      this.prerollLen -= this.preroll.shift().length
    }
  }

  queue(pcm) {
    this.pending.push(pcm)
    this.pendingLen += pcm.length
    if (this.pendingLen >= (this.inRate * SEND_MS) / 1000) this.flush()
  }

  flush() {
    if (!this.pendingLen) return
    const all = new Int16Array(this.pendingLen)
    let off = 0
    for (const p of this.pending) { all.set(p, off); off += p.length }
    this.pending = []
    this.pendingLen = 0
    this.send({ type: 'audio', audio: int16ToBase64(all) })
  }

  // Start a user turn. Interrupts a reply in progress.
  beginTurn() {
    if (this.closed) return
    if (this.responding || this.sources.size) this.interrupt()
    this.capturing = true
    for (const p of this.preroll) this.queue(p)
    this.preroll = []
    this.prerollLen = 0
    this.setState('user-speaking')
  }

  // End the user turn and ask for a reply.
  endTurn() {
    if (!this.capturing) return
    this.capturing = false
    this.flush()
    this.send({ type: 'turn.end' })
    this.setState('thinking')
  }

  sendText(text) {
    if (this.responding || this.sources.size) this.interrupt()
    this.send({ type: 'text', text })
    this.setState('thinking')
  }

  // Stop the reply: cancel upstream and drop queued audio.
  interrupt() {
    this.send({ type: 'cancel' })
    this.responding = false
    for (const s of this.sources) { try { s.stop() } catch {} }
    this.sources.clear()
    this.playhead = 0
  }

  play(b64) {
    if (!this.ctx) return
    const pcm = base64ToInt16(b64)
    if (!pcm.length) return
    const buf = this.ctx.createBuffer(1, pcm.length, this.outRate)
    buf.copyToChannel(int16ToFloat(pcm), 0)
    const src = this.ctx.createBufferSource()
    src.buffer = buf
    src.connect(this.ctx.destination)
    const now = this.ctx.currentTime
    this.playhead = Math.max(this.playhead, now + 0.03)
    src.start(this.playhead)
    this.playhead += buf.duration
    this.sources.add(src)
    this.setState('assistant-speaking')
    src.onended = () => {
      this.sources.delete(src)
      if (!this.sources.size && !this.responding && this.state === 'assistant-speaking') this.setState('listening')
    }
  }

  send(ev) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(ev))
  }

  onClosed() {
    if (this.closed) return
    this.stop()
  }

  stop() {
    if (this.closed) return
    this.closed = true
    try { this.ws?.close() } catch {}
    for (const s of this.sources) { try { s.stop() } catch {} }
    this.sources.clear()
    this.stream?.getTracks().forEach(t => t.stop())
    try { this.node?.disconnect() } catch {}
    this.ctx?.close().catch(() => {})
    this.setState('closed')
  }
}
