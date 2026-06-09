<script>
  let { title, hint = null, error = null, onBack, actionLabel = null, onAction = null,
        background = 'var(--bg)', children } = $props()
</script>

<div class="page" style="background:{background}">
  <div class="header">
    <button class="back-btn" onclick={onBack}>← Back</button>
    <h2>{title}</h2>
    {#if actionLabel}
      <button class="create-btn" onclick={onAction}>{actionLabel}</button>
    {/if}
  </div>
  {#if hint}
    <p class="hint">{hint}</p>
  {/if}
  {#if error}
    <div class="error-bar">{error}</div>
  {/if}
  {@render children?.()}
</div>

<style>
  .page {
    flex: 1; padding: 2rem; overflow-y: auto; color: var(--text);
  }

  .header {
    display: flex; align-items: center; gap: 1rem; margin-bottom: 0.5rem;
  }

  h2 { margin: 0; font-size: 1.25rem; flex: 1; color: var(--text); }

  .back-btn {
    background: none; border: none; color: var(--text3);
    cursor: pointer; font-size: 0.9rem; padding: 0.25rem 0.5rem;
    transition: color 0.15s;
  }
  .back-btn:hover { color: var(--text); }

  .create-btn {
    background: var(--accent); color: var(--bg); border: none;
    border-radius: 6px; padding: 0.4rem 0.9rem;
    cursor: pointer; font-size: 0.85rem; transition: background 0.15s;
  }
  .create-btn:hover { background: var(--accent-light); }

  .hint { color: var(--text3); font-size: 0.85rem; margin: 0 0 1.5rem; }

  .error-bar {
    background: var(--danger-bg); color: var(--danger);
    padding: 0.6rem 1rem; border-radius: 6px;
    margin-bottom: 1rem; font-size: 0.85rem;
  }

  .page :global(.loading),
  .page :global(.empty) {
    color: var(--text3); padding: 2rem 0; text-align: center;
  }
</style>
