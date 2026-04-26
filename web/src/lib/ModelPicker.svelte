<script>
  import { onMount } from 'svelte'

  let { sessionId } = $props()

  let providers     = $state([])
  let currentModel  = $state('')  // "provider/model"
  let isOpen        = $state(false)
  let query         = $state('')
  let dropdownEl    = $state(null)
  let searchEl      = $state(null)

  onMount(async () => {
    await Promise.all([loadModels(), loadConfig()])
  })

  async function loadModels() {
    try {
      const res = await fetch('/api/models')
      if (res.ok) {
        const data = await res.json()
        providers = data.providers ?? []
      }
    } catch (_) {}
  }

  async function loadConfig() {
    try {
      const res = await fetch(`/api/sessions/${sessionId}/config`)
      if (res.ok) {
        const cfg = await res.json()
        if (cfg.provider && cfg.model) currentModel = `${cfg.provider}/${cfg.model}`
        else if (cfg.provider)         currentModel = cfg.provider
      }
    } catch (_) {}
  }

  async function selectModel(providerName, modelName) {
    currentModel = `${providerName}/${modelName}`
    isOpen = false
    query  = ''
    try {
      await fetch(`/api/sessions/${sessionId}/config`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider: providerName, model: modelName }),
      })
    } catch (_) {}
  }

  function open() {
    isOpen = true
    setTimeout(() => searchEl?.focus(), 10)
  }

  function close() { isOpen = false; query = '' }

  function onOutsideClick(e) {
    if (dropdownEl && !dropdownEl.contains(e.target)) close()
  }

  let filtered = $derived.by(() => {
    const q = query.trim().toLowerCase()
    if (!q) return providers
    return providers.map(p => ({
      ...p,
      models: p.models.filter(m =>
        m.toLowerCase().includes(q) || p.name.toLowerCase().includes(q)
      ),
    })).filter(p => p.models.length > 0)
  })

  let display = $derived.by(() => {
    if (currentModel) {
      const idx = currentModel.indexOf('/')
      if (idx >= 0) return { provider: currentModel.slice(0, idx), model: currentModel.slice(idx + 1) }
      return { provider: currentModel, model: '' }
    }
    const p = providers[0]
    if (p?.models?.length) return { provider: p.name, model: p.models[0] }
    return { provider: 'default', model: '' }
  })
</script>

<svelte:window onclick={onOutsideClick} />

<div class="picker-wrap">
  <button
    class="pill"
    class:active={isOpen}
    onclick={e => { e.stopPropagation(); isOpen ? close() : open() }}
    title="Switch model"
  >
    <span class="provider">{display.provider}</span>
    <span class="slash">/</span>
    <span class="model">{display.model}</span>
    <svg class="chevron" width="9" height="9" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
      <path d="M2 4l3 3 3-3"/>
    </svg>
  </button>

  {#if isOpen}
    <div class="dropdown" bind:this={dropdownEl} onclick={e => e.stopPropagation()}>
      <div class="search-bar">
        <div class="search-inner">
          <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
            <circle cx="5" cy="5" r="3.5"/><path d="M8 8l2.5 2.5"/>
          </svg>
          <input
            bind:this={searchEl}
            bind:value={query}
            placeholder="Search models…"
            class="search-input"
          />
          {#if query}
            <button class="clear-btn" onclick={() => query = ''}>✕</button>
          {/if}
        </div>
      </div>

      <div class="model-list">
        {#if filtered.length === 0}
          <div class="empty">No models match</div>
        {/if}
        {#each filtered as prov, pi}
          {#if pi > 0}<div class="divider"></div>{/if}
          <div class="prov-label">{prov.name}</div>
          {#each prov.models as model}
            {@const key = `${prov.name}/${model}`}
            <button
              class="model-row"
              class:selected={currentModel === key}
              onclick={() => selectModel(prov.name, model)}
            >
              <span>{model}</span>
              {#if currentModel === key}
                <span class="checkmark">✓</span>
              {/if}
            </button>
          {/each}
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .picker-wrap { position: relative; }

  .pill {
    display: flex; align-items: center; gap: 0.3rem;
    background: none; border: 1px solid var(--border);
    border-radius: 6px; padding: 0.28rem 0.55rem;
    cursor: pointer; line-height: 1;
    transition: background 0.15s;
  }
  .pill:hover:not(.active) { background: var(--hover); }
  .pill.active { background: var(--surface-hi); }

  .provider { color: var(--text4); font-size: 0.68rem; font-family: system-ui, sans-serif; }
  .slash    { color: var(--border); font-size: 0.75rem; font-family: monospace; }
  .model    { color: var(--text2); font-size: 0.75rem; font-family: monospace; }
  .chevron  { color: var(--text4); margin-left: 1px; flex-shrink: 0; }

  .dropdown {
    position: absolute; top: calc(100% + 6px); right: 0;
    background: var(--surface); border: 1px solid var(--border);
    border-radius: 10px; box-shadow: 0 8px 32px rgba(0,0,0,0.45);
    min-width: 256px; z-index: 200;
    display: flex; flex-direction: column; overflow: hidden;
  }

  .search-bar { padding: 0.5rem 0.6rem; border-bottom: 1px solid var(--border); flex-shrink: 0; }
  .search-inner {
    display: flex; align-items: center; gap: 0.4rem;
    background: var(--bg); border: 1px solid var(--border);
    border-radius: 6px; padding: 0.3rem 0.55rem; color: var(--text4);
  }
  .search-input {
    background: none; border: none; outline: none;
    color: var(--text); font-size: 0.8rem; width: 100%; font-family: inherit;
  }
  .clear-btn {
    background: none; border: none; color: var(--text4);
    cursor: pointer; padding: 0; font-size: 0.75rem; line-height: 1;
  }

  .model-list { overflow-y: auto; max-height: 320px; padding: 0.3rem 0; }

  .empty { padding: 0.75rem 1rem; font-size: 0.8rem; color: var(--text4); text-align: center; }

  .divider { height: 1px; background: var(--border); margin: 0.3rem 0; }

  .prov-label {
    padding: 0.3rem 0.85rem 0.15rem;
    font-size: 0.67rem; font-weight: 700;
    letter-spacing: 0.08em; text-transform: uppercase; color: var(--text4);
  }

  .model-row {
    display: flex; align-items: center; justify-content: space-between;
    width: 100%; background: none; border: none;
    color: var(--text2); padding: 0.42rem 0.85rem 0.42rem 1.1rem;
    cursor: pointer; font-size: 0.82rem; font-family: monospace;
    text-align: left; transition: background 0.1s;
  }
  .model-row:hover    { background: var(--hover); }
  .model-row.selected { color: var(--accent-light); }

  .checkmark { font-size: 0.7rem; color: var(--accent); }
</style>
