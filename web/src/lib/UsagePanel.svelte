<script>
  import { onMount } from 'svelte'

  let usage   = $state(null)
  let loading = $state(true)
  let unavailable = $state(false)

  onMount(async () => {
    try {
      const res = await fetch('/api/sessions/usage')
      if (res.ok) {
        const data = await res.json()
        if (data.message) {
          unavailable = true
        } else {
          usage = data
        }
      }
    } catch (_) {}
    loading = false
  })

  function fmt(n) {
    if (n == null) return '0'
    return n.toLocaleString()
  }

  function pct(used, limit) {
    if (!limit || limit <= 0) return 0
    return Math.min(100, Math.round((used / limit) * 100))
  }
</script>

<style>
  .usage-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.5rem;
    margin-bottom: 1.5rem;
    max-width: 540px;
  }

  .card-title {
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    color: var(--text3);
    margin-bottom: 1rem;
  }

  .period { margin-bottom: 1rem; }

  .period-label {
    font-size: 0.72rem;
    color: var(--text4);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    margin-bottom: 0.5rem;
  }

  .token-rows { display: flex; flex-direction: column; gap: 0.4rem; }
  .token-row { display: flex; flex-direction: column; gap: 0.2rem; }

  .token-label-line {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
  }

  .token-label { font-size: 0.78rem; color: var(--text3); }
  .token-value { font-size: 0.85rem; color: var(--text); font-variant-numeric: tabular-nums; }

  .bar-track { height: 4px; background: var(--border); border-radius: 2px; overflow: hidden; }
  .bar-fill {
    height: 100%; background: var(--accent);
    border-radius: 2px; transition: width 0.3s ease;
  }
  .bar-fill.over { background: var(--danger); }

  .divider { border: none; border-top: 1px solid var(--border); margin: 1rem 0; }

  .unavailable { font-size: 0.82rem; color: var(--text3); font-style: italic; }

  .spinner {
    width: 18px; height: 18px;
    border: 2px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
    margin: 0.25rem 0;
  }
</style>

<div class="usage-card">
  <div class="card-title">Token Usage</div>

  {#if loading}
    <div class="spinner"></div>

  {:else if unavailable}
    <span class="unavailable">Usage tracking not available</span>

  {:else if usage}
    <!-- Today -->
    <div class="period">
      <div class="period-label">Today</div>
      <div class="token-rows">
        <div class="token-row">
          <div class="token-label-line">
            <span class="token-label">Input</span>
            <span class="token-value">
              {fmt(usage.today_input_tokens)}{usage.daily_input_limit ? ' / ' + fmt(usage.daily_input_limit) : ''}
            </span>
          </div>
          {#if usage.daily_input_limit}
            {@const p = pct(usage.today_input_tokens, usage.daily_input_limit)}
            <div class="bar-track">
              <div class="bar-fill" class:over={p >= 100} style="width:{p}%"></div>
            </div>
          {/if}
        </div>
        <div class="token-row">
          <div class="token-label-line">
            <span class="token-label">Output</span>
            <span class="token-value">
              {fmt(usage.today_output_tokens)}{usage.daily_output_limit ? ' / ' + fmt(usage.daily_output_limit) : ''}
            </span>
          </div>
          {#if usage.daily_output_limit}
            {@const p = pct(usage.today_output_tokens, usage.daily_output_limit)}
            <div class="bar-track">
              <div class="bar-fill" class:over={p >= 100} style="width:{p}%"></div>
            </div>
          {/if}
        </div>
      </div>
    </div>

    <hr class="divider" />

    <!-- This month -->
    <div class="period">
      <div class="period-label">This month</div>
      <div class="token-rows">
        <div class="token-row">
          <div class="token-label-line">
            <span class="token-label">Input</span>
            <span class="token-value">
              {fmt(usage.month_input_tokens)}{usage.monthly_input_limit ? ' / ' + fmt(usage.monthly_input_limit) : ''}
            </span>
          </div>
          {#if usage.monthly_input_limit}
            {@const p = pct(usage.month_input_tokens, usage.monthly_input_limit)}
            <div class="bar-track">
              <div class="bar-fill" class:over={p >= 100} style="width:{p}%"></div>
            </div>
          {/if}
        </div>
        <div class="token-row">
          <div class="token-label-line">
            <span class="token-label">Output</span>
            <span class="token-value">
              {fmt(usage.month_output_tokens)}{usage.monthly_output_limit ? ' / ' + fmt(usage.monthly_output_limit) : ''}
            </span>
          </div>
          {#if usage.monthly_output_limit}
            {@const p = pct(usage.month_output_tokens, usage.monthly_output_limit)}
            <div class="bar-track">
              <div class="bar-fill" class:over={p >= 100} style="width:{p}%"></div>
            </div>
          {/if}
        </div>
      </div>
    </div>
  {/if}
</div>
