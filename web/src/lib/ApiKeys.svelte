<script>
  import { onMount } from 'svelte'
  import Modal from './Modal.svelte'
  import PageLayout from './PageLayout.svelte'
  import { formatDate, apiFetch } from './utils.js'

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
      const res = await apiFetch('/api/apikeys', {
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
      const res = await apiFetch(`/api/apikeys/${id}`, { method: 'DELETE' })
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

  function copyKey() {
    navigator.clipboard.writeText(newKey)
  }
</script>

<PageLayout
  title="API Keys"
  hint="API keys let applications call the LLM proxy on your behalf. Each key is shown only once — store it somewhere safe."
  {error}
  {onBack}
  actionLabel="+ New key"
  onAction={() => { showCreate = true; newKey = null }}
>
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
</PageLayout>

<!-- Create key modal -->
{#if showCreate}
  <Modal title="New API key" onclose={() => showCreate = false} width="380px">
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
  </Modal>
{/if}

<!-- Revoke confirmation modal -->
{#if revokeId}
  <Modal title="Revoke key?" onclose={() => revokeId = null} width="380px">
    <p>This cannot be undone. Any application using this key will stop working immediately.</p>
    <div class="modal-btns">
      <button class="cancel-btn" onclick={() => revokeId = null} disabled={revoking}>Cancel</button>
      <button class="danger-btn" onclick={() => revokeKey(revokeId)} disabled={revoking}>
        {revoking ? 'Revoking…' : 'Revoke'}
      </button>
    </div>
  </Modal>
{/if}

<style>
  .reveal-box {
    background: var(--depth); border: 1px solid var(--success-bg);
    border-radius: 8px; padding: 1rem 1.25rem; margin-bottom: 1.5rem;
  }
  .reveal-label { font-size: 0.85rem; color: var(--success); margin-bottom: 0.5rem; }
  .reveal-row { display: flex; align-items: center; gap: 0.75rem; margin-bottom: 0.75rem; }
  .reveal-key {
    flex: 1; font-family: monospace; font-size: 0.85rem;
    background: var(--depth); color: var(--success);
    padding: 0.4rem 0.75rem; border-radius: 4px; word-break: break-all;
  }
  .copy-btn {
    background: var(--success-bg); color: var(--success); border: none;
    border-radius: 4px; padding: 0.35rem 0.75rem;
    cursor: pointer; font-size: 0.8rem; white-space: nowrap;
  }
  .copy-btn:hover { background: var(--success-bg-hover); }
  .dismiss-btn {
    background: none; border: none; color: var(--text3);
    cursor: pointer; font-size: 0.8rem; padding: 0; transition: color 0.15s;
  }
  .dismiss-btn:hover { color: var(--text2); }

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
    background: none; border: 1px solid var(--danger-border);
    color: var(--danger); border-radius: 4px;
    padding: 0.2rem 0.6rem; cursor: pointer; font-size: 0.8rem;
  }
  .revoke-btn:hover { background: var(--danger-bg); }

  .revoked-label { color: var(--text4); font-size: 0.8rem; }

</style>
