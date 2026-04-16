<script>
  import { onMount } from 'svelte'

  let { onBack, onOpenChat } = $props()

  let agents      = $state([])
  let loading     = $state(true)
  let error       = $state(null)

  // Create agent form
  let showCreate  = $state(false)
  let formName        = $state('')
  let formDisplayName = $state('')
  let formDescription = $state('')
  let formPrompt      = $state('')
  let formVisibility  = $state('internal')
  let formExternalUrl = $state('')
  let creating    = $state(false)
  let createError = $state(null)

  // Unpublish confirmation
  let unpublishName = $state(null)
  let unpublishing  = $state(false)

  const namePattern = /^[a-z0-9][a-z0-9-]*[a-z0-9]$/

  onMount(loadAgents)

  async function loadAgents() {
    loading = true
    error = null
    try {
      const res = await fetch('/api/agents')
      if (res.ok) {
        agents = await res.json() ?? []
      } else {
        error = `Failed to load agents (${res.status}).`
      }
    } catch (_) {
      error = 'Network error.'
    } finally {
      loading = false
    }
  }

  async function createAgent() {
    if (!formName.trim()) return
    if (!namePattern.test(formName.trim())) {
      createError = 'Name must match ^[a-z0-9][a-z0-9-]*[a-z0-9]$'
      return
    }
    creating = true
    createError = null
    try {
      const body = {
        name: formName.trim(),
        display_name: formDisplayName.trim() || formName.trim(),
        description: formDescription.trim(),
        system_prompt: formPrompt.trim(),
        visibility: formVisibility,
        external_url: formExternalUrl.trim(),
      }
      const res = await fetch('/api/agents', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        createError = data.error ?? `Error ${res.status}`
        return
      }

      // Publish the newly created agent.
      const pubRes = await fetch(`/api/agents/${body.name}/publish`, { method: 'POST' })
      if (!pubRes.ok) {
        const data = await pubRes.json().catch(() => ({}))
        createError = data.error ?? `Publish failed (${pubRes.status})`
        return
      }

      showCreate = false
      formName = ''
      formDisplayName = ''
      formDescription = ''
      formPrompt = ''
      formVisibility = 'internal'
      formExternalUrl = ''
      await loadAgents()
    } catch (_) {
      createError = 'Network error.'
    } finally {
      creating = false
    }
  }

  async function unpublishAgent(name) {
    unpublishing = true
    try {
      const res = await fetch(`/api/agents/${name}/unpublish`, { method: 'POST' })
      if (res.ok || res.status === 204) {
        unpublishName = null
        await loadAgents()
      } else {
        error = `Failed to unpublish agent (${res.status}).`
        unpublishName = null
      }
    } catch (_) {
      error = 'Network error.'
      unpublishName = null
    } finally {
      unpublishing = false
    }
  }

  async function openChat(agentName) {
    try {
      const res = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ persona: agentName, source: 'web' }),
      })
      if (!res.ok) {
        error = `Failed to create session (${res.status}).`
        return
      }
      const data = await res.json()
      onOpenChat?.(data.session_id)
    } catch (_) {
      error = 'Network error.'
    }
  }

  function formatDate(iso) {
    if (!iso) return '—'
    return new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
  }

  $derived: {
    // reset createError when form is closed
    if (!showCreate) createError = null
  }
</script>

<div class="page">
  <div class="header">
    <button class="back-btn" onclick={onBack}>← Back</button>
    <h2>Agent Registry</h2>
    <button class="create-btn" onclick={() => showCreate = true}>+ New agent</button>
  </div>

  <p class="hint">
    Published agents are available as personas when creating sessions.
  </p>

  {#if error}
    <div class="error-bar">{error}</div>
  {/if}

  {#if loading}
    <div class="loading">Loading…</div>
  {:else if agents.length === 0}
    <div class="empty">No published agents yet.</div>
  {:else}
    <table class="agents-table">
      <thead>
        <tr>
          <th>Name</th>
          <th>Display name</th>
          <th>Visibility</th>
          <th>Published</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each agents as a (a.name)}
          <tr>
            <td><code class="slug">{a.name}</code></td>
            <td>{a.display_name || '—'}</td>
            <td>
              <span class="badge" class:external={a.visibility === 'external'}>
                {a.visibility || 'internal'}
              </span>
              {#if a.external_url}
                <span class="badge proxy">Proxy</span>
              {/if}
            </td>
            <td>{formatDate(a.published_at)}</td>
            <td class="actions">
              <button class="open-btn" onclick={() => openChat(a.name)}>Open chat</button>
              <button class="unpublish-btn" onclick={() => unpublishName = a.name}>Unpublish</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<!-- Create agent modal -->
{#if showCreate}
  <div class="modal-backdrop" onclick={() => showCreate = false}>
    <div class="modal" onclick={e => e.stopPropagation()}>
      <h3>New agent</h3>
      {#if createError}
        <div class="error-bar">{createError}</div>
      {/if}
      <label>
        Slug (name) <span class="required">*</span>
        <input bind:value={formName} placeholder="e.g. my-assistant" disabled={creating} />
        <span class="field-hint">Lowercase letters, numbers, hyphens. At least 2 characters.</span>
      </label>
      <label>
        Display name <span class="optional">optional</span>
        <input bind:value={formDisplayName} placeholder="My Assistant" disabled={creating} />
      </label>
      <label>
        Description <span class="optional">optional</span>
        <input bind:value={formDescription} placeholder="What this agent does…" disabled={creating} />
      </label>
      <label>
        System prompt <span class="optional">optional</span>
        <textarea bind:value={formPrompt} rows="4" placeholder="You are…" disabled={creating}></textarea>
      </label>
      <label class="visibility-label">
        Visibility
        <div class="toggle-row">
          <button
            class="toggle-btn"
            class:active={formVisibility === 'internal'}
            onclick={() => formVisibility = 'internal'}
            disabled={creating}
          >Internal</button>
          <button
            class="toggle-btn"
            class:active={formVisibility === 'external'}
            onclick={() => formVisibility = 'external'}
            disabled={creating}
          >External</button>
        </div>
      </label>
      <label>
        External URL <span class="optional">(optional — proxy to existing ACP agent)</span>
        <input id="external-url" type="url" bind:value={formExternalUrl} placeholder="https://agents.example.com" disabled={creating} />
      </label>
      <div class="modal-btns">
        <button class="cancel-btn" onclick={() => showCreate = false} disabled={creating}>Cancel</button>
        <button
          class="confirm-btn"
          onclick={createAgent}
          disabled={creating || !formName.trim() || !namePattern.test(formName.trim())}
        >
          {creating ? 'Creating…' : 'Create & publish'}
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- Unpublish confirmation modal -->
{#if unpublishName}
  <div class="modal-backdrop" onclick={() => unpublishName = null}>
    <div class="modal" onclick={e => e.stopPropagation()}>
      <h3>Unpublish agent?</h3>
      <p>The agent <strong>{unpublishName}</strong> will no longer be available as a persona.</p>
      <div class="modal-btns">
        <button class="cancel-btn" onclick={() => unpublishName = null} disabled={unpublishing}>Cancel</button>
        <button class="danger-btn" onclick={() => unpublishAgent(unpublishName)} disabled={unpublishing}>
          {unpublishing ? 'Unpublishing…' : 'Unpublish'}
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
    background: #1a1a1a;
    color: #e0e0e0;
  }

  .header {
    display: flex;
    align-items: center;
    gap: 1rem;
    margin-bottom: 0.5rem;
  }

  h2 { margin: 0; font-size: 1.25rem; flex: 1; }

  .back-btn {
    background: none;
    border: none;
    color: #888;
    cursor: pointer;
    font-size: 0.9rem;
    padding: 0.25rem 0.5rem;
  }
  .back-btn:hover { color: #ccc; }

  .create-btn {
    background: #2563eb;
    color: #fff;
    border: none;
    border-radius: 6px;
    padding: 0.4rem 0.9rem;
    cursor: pointer;
    font-size: 0.85rem;
  }
  .create-btn:hover { background: #1d4ed8; }

  .hint {
    color: #888;
    font-size: 0.85rem;
    margin: 0 0 1.5rem;
  }

  .error-bar {
    background: #3b0a0a;
    color: #f87171;
    padding: 0.6rem 1rem;
    border-radius: 6px;
    margin-bottom: 1rem;
    font-size: 0.85rem;
  }

  .loading, .empty {
    color: #6b7280;
    padding: 2rem 0;
    text-align: center;
  }

  .agents-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.875rem;
  }
  .agents-table th {
    text-align: left;
    color: #6b7280;
    font-weight: 500;
    padding: 0.5rem 0.75rem;
    border-bottom: 1px solid #2a2a2a;
  }
  .agents-table td {
    padding: 0.6rem 0.75rem;
    border-bottom: 1px solid #222;
    vertical-align: middle;
  }

  .slug {
    font-family: monospace;
    font-size: 0.8rem;
    background: #2a2a2a;
    padding: 0.15rem 0.4rem;
    border-radius: 3px;
  }

  .badge {
    font-size: 0.75rem;
    padding: 0.15rem 0.5rem;
    border-radius: 3px;
    background: #1e2a1e;
    color: #86efac;
    border: 1px solid #166534;
  }
  .badge.external {
    background: #1e253a;
    color: #93c5fd;
    border-color: #1d4ed8;
  }
  .badge.proxy {
    background: #2a1e3a;
    color: #c4b5fd;
    border-color: #7c3aed;
    margin-left: 0.25rem;
  }

  .actions {
    display: flex;
    gap: 0.5rem;
    white-space: nowrap;
  }

  .open-btn {
    background: #2563eb;
    color: #fff;
    border: none;
    border-radius: 4px;
    padding: 0.2rem 0.6rem;
    cursor: pointer;
    font-size: 0.8rem;
  }
  .open-btn:hover { background: #1d4ed8; }

  .unpublish-btn {
    background: none;
    border: 1px solid #4b1c1c;
    color: #f87171;
    border-radius: 4px;
    padding: 0.2rem 0.6rem;
    cursor: pointer;
    font-size: 0.8rem;
  }
  .unpublish-btn:hover { background: #3b0a0a; }

  /* Modals */
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }
  .modal {
    background: #222;
    border: 1px solid #333;
    border-radius: 10px;
    padding: 1.5rem;
    width: 480px;
    max-width: 90vw;
    max-height: 90vh;
    overflow-y: auto;
  }
  .modal h3 { margin: 0 0 1rem; font-size: 1rem; }
  .modal p  { font-size: 0.875rem; color: #aaa; margin: 0 0 1.25rem; }

  label {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    font-size: 0.85rem;
    color: #aaa;
    margin-bottom: 1rem;
  }
  label input, label textarea {
    background: #2a2a2a;
    border: 1px solid #3a3a3a;
    border-radius: 6px;
    color: #e0e0e0;
    padding: 0.5rem 0.75rem;
    font-size: 0.875rem;
    outline: none;
    font-family: inherit;
    resize: vertical;
  }
  label input:focus, label textarea:focus { border-color: #2563eb; }

  .field-hint { font-size: 0.75rem; color: #6b7280; }

  .required { color: #f87171; }
  .optional { color: #6b7280; font-size: 0.75rem; }

  .visibility-label { gap: 0.5rem; }
  .toggle-row { display: flex; gap: 0; border-radius: 6px; overflow: hidden; border: 1px solid #3a3a3a; width: fit-content; }
  .toggle-btn {
    background: #2a2a2a;
    border: none;
    color: #888;
    padding: 0.4rem 1rem;
    cursor: pointer;
    font-size: 0.85rem;
    transition: background 0.15s, color 0.15s;
  }
  .toggle-btn.active { background: #2563eb; color: #fff; }
  .toggle-btn:disabled { opacity: 0.5; cursor: not-allowed; }

  .modal-btns { display: flex; gap: 0.75rem; justify-content: flex-end; margin-top: 0.5rem; }

  .cancel-btn {
    background: #2a2a2a;
    border: 1px solid #3a3a3a;
    color: #aaa;
    border-radius: 6px;
    padding: 0.4rem 1rem;
    cursor: pointer;
    font-size: 0.875rem;
  }
  .cancel-btn:hover { background: #333; }

  .confirm-btn {
    background: #2563eb;
    border: none;
    color: #fff;
    border-radius: 6px;
    padding: 0.4rem 1rem;
    cursor: pointer;
    font-size: 0.875rem;
  }
  .confirm-btn:hover:not(:disabled) { background: #1d4ed8; }
  .confirm-btn:disabled { opacity: 0.5; cursor: not-allowed; }

  .danger-btn {
    background: #991b1b;
    border: none;
    color: #fff;
    border-radius: 6px;
    padding: 0.4rem 1rem;
    cursor: pointer;
    font-size: 0.875rem;
  }
  .danger-btn:hover:not(:disabled) { background: #7f1d1d; }
  .danger-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
