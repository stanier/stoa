<script>
  let { sessionId, open = $bindable(false) } = $props()

  // ── State ──────────────────────────────────────────────────────────────────
  let config    = $state({})
  let dirty     = $state(false)
  let saving    = $state(false)
  let saved     = $state(false)
  let providers = $state([]) // [{ name, models: [string] }]

  $effect(() => {
    if (open && sessionId) {
      loadConfig()
      loadModels()
    }
  })

  // ── Derived ────────────────────────────────────────────────────────────────

  // Models for the currently selected provider (or all models if none selected).
  let availableModels = $derived(() => {
    if (!config.provider) {
      return providers.flatMap(p => p.models)
    }
    const p = providers.find(p => p.name === config.provider)
    return p ? p.models : []
  })

  // ── API ────────────────────────────────────────────────────────────────────

  async function loadConfig() {
    try {
      const res = await fetch(`/api/sessions/${sessionId}/config`)
      if (res.ok) config = await res.json()
    } catch (_) {}
  }

  async function loadModels() {
    try {
      const res = await fetch('/api/models')
      if (res.ok) {
        const data = await res.json()
        providers = data.providers ?? []
      }
    } catch (_) {}
  }

  async function save() {
    saving = true
    try {
      const patch = {}
      if (config.temperature    != null) patch.temperature             = config.temperature
      if (config.top_p          != null) patch.top_p                   = config.top_p
      if (config.top_k          != null) patch.top_k                   = config.top_k
      if (config.max_tokens     != null) patch.max_tokens              = config.max_tokens
      if (config.enable_thinking != null) patch.enable_thinking        = config.enable_thinking
      if (config.thinking_budget_tokens != null) patch.thinking_budget_tokens = config.thinking_budget_tokens
      if (config.provider       != null) patch.provider                = config.provider
      if (config.model          != null) patch.model                   = config.model

      await fetch(`/api/sessions/${sessionId}/config`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(patch),
      })
      dirty = false
      saved = true
      setTimeout(() => saved = false, 2000)
    } finally {
      saving = false
    }
  }

  function reset() {
    config = {}
    dirty = true
  }

  function mark() { dirty = true; saved = false }

  function onProviderChange(e) {
    config.provider = e.target.value || null
    config.model = null   // clear model when provider changes
    mark()
  }

  function onModelChange(e) {
    config.model = e.target.value || null
    mark()
  }

  // ── Helpers ────────────────────────────────────────────────────────────────

  function asFloat(val) {
    const n = parseFloat(val)
    return isNaN(n) ? null : n
  }
  function asInt(val) {
    const n = parseInt(val, 10)
    return isNaN(n) ? null : n
  }
</script>

<style>
  .panel {
    width: 260px;
    flex-shrink: 0;
    background: #13161f;
    border-left: 1px solid #2d3148;
    display: flex;
    flex-direction: column;
    overflow-y: auto;
  }

  .panel-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.65rem 0.75rem;
    border-bottom: 1px solid #2d3148;
    flex-shrink: 0;
  }
  .panel-title {
    font-size: 0.72rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: #475569;
  }
  button.close {
    background: none; border: none; color: #475569; cursor: pointer;
    font-size: 1rem; padding: 0; line-height: 1;
  }
  button.close:hover { color: #e2e8f0; }

  .fields { padding: 0.75rem; display: flex; flex-direction: column; gap: 1rem; }

  .field { display: flex; flex-direction: column; gap: 0.3rem; }
  .field label {
    font-size: 0.72rem; color: #64748b;
    display: flex; justify-content: space-between; align-items: center;
  }
  .field label span { color: #94a3b8; font-family: monospace; }

  input[type=range] {
    width: 100%; accent-color: #6366f1; cursor: pointer;
  }
  input[type=number] {
    width: 100%; background: #0f1117; border: 1px solid #2d3148;
    border-radius: 5px; color: #e2e8f0; font-size: 0.85rem;
    padding: 0.35rem 0.5rem; outline: none;
    transition: border-color 0.15s;
  }
  input[type=number]:focus { border-color: #6366f1; }

  select {
    width: 100%; background: #0f1117; border: 1px solid #2d3148;
    border-radius: 5px; color: #e2e8f0; font-size: 0.82rem;
    padding: 0.35rem 0.5rem; outline: none; cursor: pointer;
    transition: border-color 0.15s;
    appearance: none;
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='8' viewBox='0 0 12 8'%3E%3Cpath fill='%2364748b' d='M1 1l5 5 5-5'/%3E%3C/svg%3E");
    background-repeat: no-repeat;
    background-position: right 0.5rem center;
    padding-right: 1.75rem;
  }
  select:focus { border-color: #6366f1; }

  .toggle-row {
    display: flex; align-items: center; justify-content: space-between;
  }
  .toggle-label { font-size: 0.8rem; color: #94a3b8; }

  /* Toggle switch */
  .switch { position: relative; width: 36px; height: 20px; flex-shrink: 0; }
  .switch input { opacity: 0; width: 0; height: 0; }
  .slider {
    position: absolute; inset: 0; background: #2d3148; border-radius: 20px;
    cursor: pointer; transition: background 0.2s;
  }
  .slider::before {
    content: ''; position: absolute; width: 14px; height: 14px;
    left: 3px; top: 3px; background: #64748b; border-radius: 50%;
    transition: transform 0.2s, background 0.2s;
  }
  input:checked + .slider { background: #6366f1; }
  input:checked + .slider::before { transform: translateX(16px); background: #fff; }

  .divider {
    border: none; border-top: 1px solid #2d3148; margin: 0;
  }

  .hint { font-size: 0.7rem; color: #334155; line-height: 1.4; }

  .footer {
    padding: 0.75rem; display: flex; gap: 0.5rem;
    border-top: 1px solid #2d3148; flex-shrink: 0;
  }
  button.save-btn {
    flex: 1; background: #6366f1; color: #fff; border: none;
    border-radius: 6px; font-size: 0.8rem; font-weight: 500;
    padding: 0.45rem; cursor: pointer; transition: background 0.15s;
  }
  button.save-btn:hover:not(:disabled) { background: #4f46e5; }
  button.save-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  button.save-btn.ok { background: #166534; }
  button.reset-btn {
    background: none; border: 1px solid #2d3148; color: #64748b;
    border-radius: 6px; font-size: 0.8rem; padding: 0.45rem 0.65rem;
    cursor: pointer; transition: color 0.15s, border-color 0.15s;
  }
  button.reset-btn:hover { color: #e2e8f0; border-color: #475569; }
</style>

{#if open}
<div class="panel">
  <div class="panel-header">
    <span class="panel-title">Model &amp; Params</span>
    <button class="close" onclick={() => open = false}>✕</button>
  </div>

  <div class="fields">

    <!-- Provider selection -->
    {#if providers.length > 0}
      <div class="field">
        <label>Provider</label>
        <select value={config.provider ?? ''} onchange={onProviderChange}>
          <option value="">default</option>
          {#each providers.filter(p => p.name !== 'default') as p}
            <option value={p.name}>{p.name}</option>
          {/each}
        </select>
      </div>

      <!-- Model selection -->
      <div class="field">
        <label>Model</label>
        <select value={config.model ?? ''} onchange={onModelChange}>
          <option value="">default</option>
          {#each availableModels() as m}
            <option value={m}>{m}</option>
          {/each}
        </select>
      </div>

      <hr class="divider" />
    {/if}

    <!-- Temperature -->
    <div class="field">
      <label>
        Temperature
        <span>{config.temperature != null ? config.temperature.toFixed(2) : 'default'}</span>
      </label>
      <input type="range" min="0" max="2" step="0.01"
        value={config.temperature ?? 1}
        oninput={e => { config.temperature = asFloat(e.target.value); mark() }}
      />
    </div>

    <!-- Top-P -->
    <div class="field">
      <label>
        Top-P
        <span>{config.top_p != null ? config.top_p.toFixed(2) : 'default'}</span>
      </label>
      <input type="range" min="0" max="1" step="0.01"
        value={config.top_p ?? 1}
        oninput={e => { config.top_p = asFloat(e.target.value); mark() }}
      />
    </div>

    <!-- Max tokens -->
    <div class="field">
      <label>Max tokens</label>
      <input type="number" min="1" max="200000" step="256"
        value={config.max_tokens ?? ''}
        placeholder="default"
        oninput={e => { config.max_tokens = asInt(e.target.value); mark() }}
      />
    </div>

    <!-- Top-K (Anthropic) -->
    <div class="field">
      <label>Top-K <span style="font-size:0.65rem;color:#334155">(Anthropic)</span></label>
      <input type="number" min="1" max="2048" step="1"
        value={config.top_k ?? ''}
        placeholder="default"
        oninput={e => { config.top_k = asInt(e.target.value); mark() }}
      />
    </div>

    <hr class="divider" />

    <!-- Enable thinking -->
    <div class="toggle-row">
      <span class="toggle-label">Extended thinking <span style="font-size:0.65rem;color:#334155">(Anthropic)</span></span>
      <label class="switch">
        <input type="checkbox"
          checked={config.enable_thinking ?? false}
          onchange={e => { config.enable_thinking = e.target.checked; mark() }}
        />
        <span class="slider"></span>
      </label>
    </div>

    {#if config.enable_thinking}
      <div class="field">
        <label>Thinking budget tokens</label>
        <input type="number" min="1024" max="100000" step="1024"
          value={config.thinking_budget_tokens ?? 10000}
          oninput={e => { config.thinking_budget_tokens = asInt(e.target.value); mark() }}
        />
        <p class="hint">Counts against max tokens. Minimum 1,024.</p>
      </div>
    {/if}

    <p class="hint">
      Unset fields use the server default.<br>
      Changes take effect on the next message.
    </p>

  </div>

  <div class="footer">
    <button class="reset-btn" onclick={reset} title="Reset to server defaults">Reset</button>
    <button class="save-btn" class:ok={saved}
      onclick={save} disabled={!dirty || saving}>
      {saved ? '✓ Saved' : saving ? 'Saving…' : 'Apply'}
    </button>
  </div>
</div>
{/if}
