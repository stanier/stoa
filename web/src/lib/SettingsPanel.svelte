<script>
  import SidePanel from './SidePanel.svelte'
  import { apiFetch } from './utils.js'

  let { sessionId, open = $bindable(false) } = $props()

  let config    = $state({})
  let dirty     = $state(false)
  let saving    = $state(false)
  let saved     = $state(false)
  let providers = $state([])

  $effect(() => {
    if (open && sessionId) {
      loadConfig()
      loadModels()
    }
  })

  let availableModels = $derived.by(() => {
    if (!config.provider) return providers.flatMap(p => p.models)
    const p = providers.find(p => p.name === config.provider)
    return p ? p.models : []
  })

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
      if (config.enable_thinking     != null) patch.enable_thinking         = config.enable_thinking
      if (config.enable_oai_thinking != null) patch.enable_oai_thinking     = config.enable_oai_thinking
      if (config.thinking_budget_tokens != null) patch.thinking_budget_tokens = config.thinking_budget_tokens
      if (config.provider       != null) patch.provider                = config.provider
      if (config.model          != null) patch.model                   = config.model

      await apiFetch(`/api/sessions/${sessionId}/config`, {
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

  function reset() { config = {}; dirty = true }
  function mark() { dirty = true; saved = false }

  function onProviderChange(e) {
    config.provider = e.target.value || null
    config.model = null
    mark()
  }

  function onModelChange(e) {
    config.model = e.target.value || null
    mark()
  }

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
  .fields { padding: 0.75rem; display: flex; flex-direction: column; gap: 1rem; }

  .field { display: flex; flex-direction: column; gap: 0.3rem; }
  .field label {
    font-size: 0.72rem; color: var(--text3);
    display: flex; justify-content: space-between; align-items: center;
  }
  .field label span { color: var(--text2); font-family: monospace; }

  input[type=range] { width: 100%; accent-color: var(--accent); cursor: pointer; }
  input[type=number] {
    width: 100%; background: var(--depth); border: 1px solid var(--border);
    border-radius: 5px; color: var(--text); font-size: 0.85rem;
    padding: 0.35rem 0.5rem; outline: none;
    transition: border-color 0.15s;
  }
  input[type=number]:focus { border-color: var(--accent); }

  select {
    width: 100%; background: var(--depth); border: 1px solid var(--border);
    border-radius: 5px; color: var(--text); font-size: 0.82rem;
    padding: 0.35rem 0.5rem; outline: none; cursor: pointer;
    transition: border-color 0.15s; appearance: none;
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='8' viewBox='0 0 12 8'%3E%3Cpath fill='%236b6560' d='M1 1l5 5 5-5'/%3E%3C/svg%3E");
    background-repeat: no-repeat;
    background-position: right 0.5rem center;
    padding-right: 1.75rem;
  }
  select:focus { border-color: var(--accent); }

  .toggle-row { display: flex; align-items: center; justify-content: space-between; }
  .toggle-label { font-size: 0.8rem; color: var(--text2); }

  .switch { position: relative; width: 36px; height: 20px; flex-shrink: 0; }
  .switch input { opacity: 0; width: 0; height: 0; }
  .slider {
    position: absolute; inset: 0; background: var(--border); border-radius: 20px;
    cursor: pointer; transition: background 0.2s;
  }
  .slider::before {
    content: ''; position: absolute; width: 14px; height: 14px;
    left: 3px; top: 3px; background: var(--text3); border-radius: 50%;
    transition: transform 0.2s, background 0.2s;
  }
  input:checked + .slider { background: var(--accent); }
  input:checked + .slider::before { transform: translateX(16px); background: var(--bg); }

  .divider { border: none; border-top: 1px solid var(--border); margin: 0; }

  .hint { font-size: 0.7rem; color: var(--text4); line-height: 1.4; }

  .footer {
    padding: 0.75rem; display: flex; gap: 0.5rem; flex-shrink: 0;
  }
  button.save-btn {
    flex: 1; background: var(--accent); color: var(--bg); border: none;
    border-radius: 6px; font-size: 0.8rem; font-weight: 500;
    padding: 0.45rem; cursor: pointer; transition: background 0.15s;
  }
  button.save-btn:hover:not(:disabled) { background: var(--accent-light); }
  button.save-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  button.save-btn.ok { background: var(--success-bg); color: #fff; }
  button.reset-btn {
    background: none; border: 1px solid var(--border); color: var(--text3);
    border-radius: 6px; font-size: 0.8rem; padding: 0.45rem 0.65rem;
    cursor: pointer; transition: color 0.15s, border-color 0.15s;
  }
  button.reset-btn:hover { color: var(--text); border-color: var(--text4); }
</style>

<SidePanel title="Model & Params" bind:open>
  <div class="fields">

    {#if providers.length > 0}
      <div class="field">
        <label for="sp-provider">Provider</label>
        <select id="sp-provider" value={config.provider ?? ''} onchange={onProviderChange}>
          <option value="">default</option>
          {#each providers.filter(p => p.name !== 'default') as p}
            <option value={p.name}>{p.name}</option>
          {/each}
        </select>
      </div>

      <div class="field">
        <label for="sp-model">Model</label>
        <select id="sp-model" value={config.model ?? ''} onchange={onModelChange}>
          <option value="">default</option>
          {#each availableModels as m}
            <option value={m}>{m}</option>
          {/each}
        </select>
      </div>

      <hr class="divider" />
    {/if}

    <div class="field">
      <label for="sp-temperature">
        Temperature
        <span>{config.temperature != null ? config.temperature.toFixed(2) : 'default'}</span>
      </label>
      <input id="sp-temperature" type="range" min="0" max="2" step="0.01"
        value={config.temperature ?? 1}
        oninput={e => { config.temperature = asFloat(e.target.value); mark() }}
      />
    </div>

    <div class="field">
      <label for="sp-top-p">
        Top-P
        <span>{config.top_p != null ? config.top_p.toFixed(2) : 'default'}</span>
      </label>
      <input id="sp-top-p" type="range" min="0" max="1" step="0.01"
        value={config.top_p ?? 1}
        oninput={e => { config.top_p = asFloat(e.target.value); mark() }}
      />
    </div>

    <div class="field">
      <label for="sp-max-tokens">Max tokens</label>
      <input id="sp-max-tokens" type="number" min="1" max="200000" step="256"
        value={config.max_tokens ?? ''}
        placeholder="default"
        oninput={e => { config.max_tokens = asInt(e.target.value); mark() }}
      />
    </div>

    <div class="field">
      <label for="sp-top-k">Top-K <span style="font-size:0.65rem;color:var(--text5)">(Anthropic)</span></label>
      <input id="sp-top-k" type="number" min="1" max="2048" step="1"
        value={config.top_k ?? ''}
        placeholder="default"
        oninput={e => { config.top_k = asInt(e.target.value); mark() }}
      />
    </div>

    <hr class="divider" />

    <div class="toggle-row">
      <span class="toggle-label">Extended thinking <span style="font-size:0.65rem;color:var(--text5)">(Anthropic / o-series)</span></span>
      <label class="switch">
        <input type="checkbox"
          checked={config.enable_thinking ?? false}
          onchange={e => { config.enable_thinking = e.target.checked; mark() }}
        />
        <span class="slider"></span>
      </label>
    </div>

    <div class="toggle-row">
      <span class="toggle-label">Request thinking <span style="font-size:0.65rem;color:var(--text5)">(OpenAI-compatible)</span></span>
      <label class="switch">
        <input type="checkbox"
          checked={config.enable_oai_thinking ?? false}
          onchange={e => { config.enable_oai_thinking = e.target.checked; mark() }}
        />
        <span class="slider"></span>
      </label>
    </div>

    {#if config.enable_thinking || config.enable_oai_thinking}
      <div class="field">
        <label for="sp-thinking-budget">Thinking budget tokens</label>
        <input id="sp-thinking-budget" type="number" min="1024" max="100000" step="1024"
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
</SidePanel>
