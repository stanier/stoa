<script>
  let { title, open = $bindable(false), children } = $props()
</script>

<div class="panel">
  <div class="panel-header">
    <span class="panel-title">{title}</span>
    <button class="close" onclick={() => open = false}>✕</button>
  </div>
  {@render children?.()}
</div>

<style>
  .panel {
    position: absolute; inset: 0; height: 100%;
    background: var(--sidebar);
    display: flex; flex-direction: column; overflow-y: auto;
  }

  .panel-header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 0.65rem 0.75rem; flex-shrink: 0;
  }

  .panel-title {
    font-size: 0.72rem; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.06em; color: var(--text4);
  }

  button.close {
    background: none; border: none; color: var(--text4); cursor: pointer;
    font-size: 1rem; padding: 0; line-height: 1; transition: color 0.15s;
  }
  button.close:hover { color: var(--text); }
</style>
