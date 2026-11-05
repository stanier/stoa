<script>
  // Voice mode for omni models: a strip above the composer that runs one
  // realtime voice-to-voice conversation. Transcripts go back to Chat through
  // callbacks so they show in the message list.
  //
  // Voice turns are not saved to the session's history yet; they live in the
  // page until it reloads.
  import { onMount, onDestroy } from 'svelte'
  import { VoiceSession } from './voice/session.js'

  let { provider, model, onUserTranscript, onAssistantDelta, onAssistantDone, onClose } = $props()

  let session   = null
  let vstate    = $state('idle')
  let level     = $state(0)
  let handsFree = $state(true)
  let error     = $state(null)
  let talking   = $state(false)

  const LABELS = {
    idle: 'Starting…', connecting: 'Connecting…', listening: 'Listening',
    'user-speaking': 'Hearing you…', thinking: 'Thinking…',
    'assistant-speaking': 'Speaking', closed: 'Voice off',
  }

  async function start() {
    error = null
    session = new VoiceSession({
      provider, model, handsFree,
      on: {
        state: s => { vstate = s },
        level: l => { level = l },
        transcript: t => onUserTranscript?.(t),
        textDelta: d => onAssistantDelta?.(d),
        responseDone: () => onAssistantDone?.(),
        error: m => { error = m },
      },
    })
    try {
      await session.start()
    } catch (e) {
      error = e?.name === 'NotAllowedError' ? 'Microphone permission was denied.' : (e?.message ?? 'Could not start voice.')
      session.stop()
    }
  }

  function stop() {
    session?.stop()
    onClose?.()
  }

  function toggleHandsFree() {
    handsFree = !handsFree
    if (session) {
      session.handsFree = handsFree
      session.vad.reset()
    }
  }

  function pttDown() { if (!handsFree && session) { talking = true; session.beginTurn() } }
  function pttUp()   { if (!handsFree && session && talking) { talking = false; session.endTurn() } }

  onMount(start)
  onDestroy(() => session?.stop())
</script>

<div class="voice-panel" class:err={!!error}>
  <span class="dot" class:live={vstate === 'user-speaking'} class:speaking={vstate === 'assistant-speaking'}
        style="--lvl: {Math.min(1, level * 12)}"></span>
  <span class="label">{error ?? LABELS[vstate] ?? vstate}</span>
  <span class="model" title="Voice model">{model}</span>
  <span class="spacer"></span>
  {#if !handsFree}
    <button class="ptt" class:down={talking}
      onpointerdown={pttDown} onpointerup={pttUp} onpointerleave={pttUp}
      disabled={vstate === 'closed' || vstate === 'connecting'}>
      {talking ? 'Release to send' : 'Hold to talk'}
    </button>
  {/if}
  <button class="mode" onclick={toggleHandsFree}
    title={handsFree ? 'Ends your turn when you pause; speak over a reply to interrupt it' : 'Hold the button while you speak'}>
    {handsFree ? 'Hands-free' : 'Push to talk'}
  </button>
  {#if vstate === 'closed' || error}
    <button class="mode" onclick={() => { session?.stop(); start() }}>Retry</button>
  {/if}
  <button class="end" onclick={stop} title="End voice mode">End</button>
</div>

<style>
  .voice-panel {
    display: flex; align-items: center; gap: 0.6rem;
    padding: 0.45rem 1.25rem; background: var(--surface-hi);
    border-top: 1px solid var(--border); font-size: 0.82rem; color: var(--text2);
    flex-shrink: 0;
  }
  .voice-panel.err .label { color: var(--danger-light); }
  .dot {
    width: 10px; height: 10px; border-radius: 50%; flex-shrink: 0;
    background: var(--text4);
    box-shadow: 0 0 0 calc(var(--lvl, 0) * 6px) color-mix(in srgb, var(--accent) 35%, transparent);
    transition: box-shadow 0.08s, background 0.15s;
  }
  .dot.live     { background: var(--accent-light); }
  .dot.speaking { background: var(--success); }
  .model  { font-family: monospace; font-size: 0.72rem; color: var(--text4); }
  .spacer { flex: 1; }
  button {
    background: none; border: 1px solid var(--border); border-radius: 6px;
    color: var(--text2); cursor: pointer; padding: 0.25rem 0.6rem; font-size: 0.78rem;
  }
  button:hover:not(:disabled) { background: var(--hover); }
  button:disabled { opacity: 0.4; cursor: not-allowed; }
  .ptt { user-select: none; touch-action: none; }
  .ptt.down { background: var(--accent); color: var(--bg); }
  .end { color: var(--danger-light); border-color: var(--danger-border); }
</style>
