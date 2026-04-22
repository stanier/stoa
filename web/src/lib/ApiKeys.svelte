<script>
  import { onMount } from 'svelte'

  let { onBack } = $props()

  let keys        = $state([])
  let loading     = $state(true)
  let error       = $state(null)

  // Create key modal
  let showCreate  = $state(false)
  let newName     = $state('')
  let newExpiry   = $state('')   // days, optional
  let creating    = $state(false)
  let createError = $state(null)
  let newKey      = $state(null) // shown once after creation

  // Revoke confirmation
  let revokeId    = $state(null)
  let revoking    = $state(false)

  onMount(loadKeys)

  async function loadKeys() {
    loading = true
    error = null
    try {
      const res = await fetch('/api/apikeys')
      if (res.ok) {
        keys = await res.json()
      } else {
        error = `Failed to load keys (${res.status}).`
      }
    } catch (_) {
      error = 'Network error.'
    } finally {
      loading = false
    }
  }

  async function createKey() {
    if (!newName.trim()) return
    creating = true
    createError = null
    try {
      const body = { name: newName.trim() }
      if (newExpiry) body.expires_in_days = parseInt(newExpiry, 10)
      const res = await fetch('/api/apikeys', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (res.ok) {
        const data = await res.json()
        newKey = data.key
        showCreate = false
        newName = ''
        newExpiry = ''
        await loadKeys()
      } else {
        const data = await res.json().catch(() => ({}))
        createError = data.error ?? `Error ${res.status}`
      }
    } catch (_) {
      createError = 'Network error.'
    } finally {
      creating = false
    }
  }

  async function revokeKey(id) {
    revoking = true
    try {
      const res = await fetch(`/api/apikeys/${id}`, { method: 'DELETE' })
      if (res.ok || res.status === 204) {
        revokeId = null
        await loadKeys()
      } else {
        error = `Failed to revoke key (${res.status}).`
        revokeId = null
      }
    } catch (_) {
      error = 'Network error.'
      revokeId = null
    } finally {
      revoking = false
    }
  }

  function formatDate(iso) {
    if (!iso) return '—'
    return new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
  }

  function copyKey() {
    navigator.clipboard.writeText(newKey)
  }
</script>

<div class="page">
  <div class="header">
    <button class="back-btn" onclick={onBack}>← Back</button>
    <h2>API Keys</h2>
    <button class="create-btn" onclick={() => { showCreate = true; newKey = null }}>+ New key</button>
  </div>

  <p class="hint">
    API keys let applications call the LLM proxy on your behalf.
    Each key is shown only once — store it somewhere safe.
  </p>

  {#if error}
    <div class="error-bar">{error}</div>
  {/if}

  <!-- New key revealed after creation -->
  {#if newKey}
    <div class="reveal-box">
      <div class="reveal-label">Your new API key — copy it now, it won't be shown again.</div>
      <div class="reveal-row">
        <code class="reveal-key">{newKey}</code>
        <button class="copy-btn" onclick={copyKey}>Copy</button>
      </div>
      <button class="dismiss-btn" onclick={() => newKey = null}>Dismiss</button>
    </div>
  {/if}

  <!-- Key table -->
  {#if loading}
    <div class="loading">Loading…</div>
  {:else if keys.length === 0}
    <div class="empty">No API keys yet.</div>
  {:else}
    <table class="keys-table">
      <thead>
        <tr>
          <th>Name</th>
          <th>Prefix</th>
          <th>Created</th>
          <th>Last used</th>
          <th>Expires</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each keys as k (k.id)}
          <tr class:revoked={k.revoked_at}>
            <td>{k.name}</td>
            <td><code class="prefix">{k.prefix}</code></td>
            <td>{formatDate(k.created_at)}</td>
            <td>{formatDate(k.last_used_at)}</td>
            <td>{formatDate(k.expires_at)}</td>
            <td>
              {#if !k.revoked_at}
                <button class="revoke-btn" onclick={() => revokeId = k.id}>Revoke</button>
              {:else}
                <span class="revoked-label">Revoked</span>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<!-- Create key modal -->
{#if showCreate}
  <div class="modal-backdrop" onclick={() => showCreate = false}>
    <div class="modal" onclick={e => e.stopPropagation()}>
      <h3>New API key</h3>
      {#if createError}
        <div class="error-bar">{createError}</div>
      {/if}
      <label>
        Name <span class="required">*</span>
        <input bind:value={newName} placeholder="e.g. prod-app, dev-laptop" disabled={creating} />
      </label>
      <label>
        Expires in (days) <span class="optional">optional</span>
        <input type="number" bind:value={newExpiry} placeholder="leave blank for no expiry" min="1" disabled={creating} />
      </label>
      <div class="modal-btns">
        <button class="cancel-btn" onclick={() => showCreate = false} disabled={creating}>Cancel</button>
        <button class="confirm-btn" onclick={createKey} disabled={creating || !newName.trim()}>
          {creating ? 'Creating…' : 'Create'}
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- Revoke confirmation modal -->
{#if revokeId}
  <div class="modal-backdrop" onclick={() => revokeId = null}>
    <div class="modal" onclick={e => e.stopPropagation()}>
      <h3>Revoke key?</h3>
      <p>This cannot be undone. Any application using this key will stop working immediately.</p>
      <div class="modal-btns">
        <button class="cancel-btn" onclick={() => revokeId = null} disabled={revoking}>Cancel</button>
        <button class="danger-btn" onclick={() => revokeKey(revokeId)} disabled={revoking}>
          {revoking ? 'Revoking…' : 'Revoke'}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page {
    flex: 1;
    padding: 2rem;
    overflow-y: auto;
    background: var(--bg);
    color: var(--text);
  }

  .header {
    display: flex;
    align-items: center;
    gap: 1rem;
    margin-bottom: 0.5rem;
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
    background: #3b0a0a; color: #f87171;
    padding: 0.6rem 1rem; border-radius: 6px;
    margin-bottom: 1rem; font-size: 0.85rem;
  }

  .reveal-box {
    background: var(--depth); border: 1px solid #166534;
    border-radius: 8px; padding: 1rem 1.25rem; margin-bottom: 1.5rem;
  }
  .reveal-label { font-size: 0.85rem; color: #86efac; margin-bottom: 0.5rem; }
  .reveal-row { display: flex; align-items: center; gap: 0.75rem; margin-bottom: 0.75rem; }
  .reveal-key {
    flex: 1; font-family: monospace; font-size: 0.85rem;
    background: var(--depth); color: #86efac;
    padding: 0.4rem 0.75rem; border-radius: 4px; word-break: break-all;
  }
  .copy-btn {
    background: #166534; color: #86efac; border: none;
    border-radius: 4px; padding: 0.35rem 0.75rem;
    cursor: pointer; font-size: 0.8rem; white-space: nowrap;
  }
  .copy-btn:hover { background: #15803d; }
  .dismiss-btn {
    background: none; border: none; color: var(--text3);
    cursor: pointer; font-size: 0.8rem; padding: 0; transition: color 0.15s;
  }
  .dismiss-btn:hover { color: var(--text2); }

  .loading, .empty { color: var(--text3); padding: 2rem 0; text-align: center; }

  .keys-table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
  .keys-table th {
    text-align: left; color: var(--text3); font-weight: 500;
    padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--border);
  }
  .keys-table td {
    padding: 0.6rem 0.75rem; border-bottom: 1px solid var(--depth);
    vertical-align: middle; color: var(--text2);
  }
  .keys-table tr.revoked td { opacity: 0.45; }

  .prefix {
    font-family: monospace; font-size: 0.8rem;
    background: var(--surface-hi); color: var(--text2);
    padding: 0.15rem 0.4rem; border-radius: 3px;
  }

  .revoke-btn {
    background: none; border: 1px solid #4b1c1c;
    color: #f87171; border-radius: 4px;
    padding: 0.2rem 0.6rem; cursor: pointer; font-size: 0.8rem;
  }
  .revoke-btn:hover { background: #3b0a0a; }

  .revoked-label { color: var(--text4); font-size: 0.8rem; }

  .modal-backdrop {
    position: fixed; inset: 0; background: rgba(0,0,0,0.6);
    display: flex; align-items: center; justify-content: center; z-index: 100;
  }
  .modal {
    background: var(--surface); border: 1px solid var(--border);
    border-radius: 10px; padding: 1.5rem; width: 380px; max-width: 90vw;
  }
  .modal h3 { margin: 0 0 1rem; font-size: 1rem; color: var(--text); }
  .modal p  { font-size: 0.875rem; color: var(--text2); margin: 0 0 1.25rem; }

  label {
    display: flex; flex-direction: column; gap: 0.35rem;
    font-size: 0.85rem; color: var(--text2); margin-bottom: 1rem;
  }
  label input {
    background: var(--depth); border: 1px solid var(--border);
    border-radius: 6px; color: var(--text);
    padding: 0.5rem 0.75rem; font-size: 0.875rem; outline: none;
    transition: border-color 0.15s;
  }
  label input:focus { border-color: var(--accent); }

  .required { color: #f87171; }
  .optional { color: var(--text4); font-size: 0.75rem; }

  .modal-btns { display: flex; gap: 0.75rem; justify-content: flex-end; }

  .cancel-btn {
    background: var(--surface-hi); border: 1px solid var(--border);
    color: var(--text2); border-radius: 6px;
    padding: 0.4rem 1rem; cursor: pointer; font-size: 0.875rem;
    transition: background 0.15s;
  }
  .cancel-btn:hover { background: var(--hover); }

  .confirm-btn {
    background: var(--accent); border: none; color: var(--bg);
    border-radius: 6px; padding: 0.4rem 1rem;
    cursor: pointer; font-size: 0.875rem; transition: background 0.15s;
  }
  .confirm-btn:hover:not(:disabled) { background: var(--accent-light); }
  .confirm-btn:disabled { opacity: 0.5; cursor: not-allowed; }

  .danger-btn {
    background: #991b1b; border: none; color: #fff;
    border-radius: 6px; padding: 0.4rem 1rem;
    cursor: pointer; font-size: 0.875rem;
  }
  .danger-btn:hover:not(:disabled) { background: #7f1d1d; }
  .danger-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
