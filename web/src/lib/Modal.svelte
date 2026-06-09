<script>
  let { title, onclose, children, width = '480px' } = $props()
</script>

<div class="modal-backdrop" role="presentation" onclick={onclose} onkeydown={e => e.key === 'Escape' && onclose?.()}>
  <div class="modal" role="dialog" aria-modal="true" tabindex="-1" style="width:{width}" onclick={e => e.stopPropagation()} onkeydown={e => e.stopPropagation()}>
    {#if title}<h3>{title}</h3>{/if}
    {@render children?.()}
  </div>
</div>

<style>
  .modal-backdrop {
    position: fixed; inset: 0; background: rgba(0,0,0,0.6);
    display: flex; align-items: center; justify-content: center; z-index: 100;
  }
  .modal {
    background: var(--surface); border: 1px solid var(--border);
    border-radius: 10px; padding: 1.5rem;
    max-width: 90vw; max-height: 90vh; overflow-y: auto;
  }
  .modal :global(h3) { margin: 0 0 1rem; font-size: 1rem; color: var(--text); }
  .modal :global(p)  { font-size: 0.875rem; color: var(--text2); margin: 0 0 1.25rem; }

  .modal :global(label) {
    display: flex; flex-direction: column; gap: 0.35rem;
    font-size: 0.85rem; color: var(--text2); margin-bottom: 1rem;
  }
  .modal :global(label input),
  .modal :global(label textarea) {
    background: var(--depth); border: 1px solid var(--border);
    border-radius: 6px; color: var(--text);
    padding: 0.5rem 0.75rem; font-size: 0.875rem; outline: none;
    font-family: inherit; resize: vertical; transition: border-color 0.15s;
  }
  .modal :global(label input:focus),
  .modal :global(label textarea:focus) { border-color: var(--accent); }

  .modal :global(.field-hint) { font-size: 0.75rem; color: var(--text4); }
  .modal :global(.required)   { color: var(--danger); }
  .modal :global(.optional)   { color: var(--text4); font-size: 0.75rem; }

  .modal :global(.error-bar) {
    background: var(--danger-bg); color: var(--danger);
    padding: 0.6rem 1rem; border-radius: 6px;
    margin-bottom: 1rem; font-size: 0.85rem;
  }

  .modal :global(.modal-btns) {
    display: flex; gap: 0.75rem; justify-content: flex-end; margin-top: 0.5rem;
  }

  .modal :global(.cancel-btn) {
    background: var(--surface-hi); border: 1px solid var(--border);
    color: var(--text2); border-radius: 6px; padding: 0.4rem 1rem;
    cursor: pointer; font-size: 0.875rem; transition: background 0.15s;
  }
  .modal :global(.cancel-btn:hover) { background: var(--hover); }

  .modal :global(.confirm-btn) {
    background: var(--accent); border: none; color: var(--bg);
    border-radius: 6px; padding: 0.4rem 1rem;
    cursor: pointer; font-size: 0.875rem; transition: background 0.15s;
  }
  .modal :global(.confirm-btn:hover:not(:disabled)) { background: var(--accent-light); }
  .modal :global(.confirm-btn:disabled) { opacity: 0.5; cursor: not-allowed; }

  .modal :global(.danger-btn) {
    background: var(--danger-btn); border: none; color: #fff;
    border-radius: 6px; padding: 0.4rem 1rem;
    cursor: pointer; font-size: 0.875rem;
  }
  .modal :global(.danger-btn:hover:not(:disabled)) { background: var(--danger-btn-hover); }
  .modal :global(.danger-btn:disabled) { opacity: 0.5; cursor: not-allowed; }
</style>
