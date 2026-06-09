<script>
  import { onMount } from 'svelte'
  import Modal from './Modal.svelte'
  import PageLayout from './PageLayout.svelte'
  import { formatDate, apiFetch } from './utils.js'

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
      const res = await apiFetch('/api/agents', {
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
      const pubRes = await apiFetch(`/api/agents/${body.name}/publish`, { method: 'POST' })
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
      const res = await apiFetch(`/api/agents/${name}/unpublish`, { method: 'POST' })
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
      const res = await apiFetch('/api/sessions', {
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

  $effect(() => { if (!showCreate) createError = null })
</script>

<PageLayout
  title="Agent Registry"
  hint="Published agents are available as personas when creating sessions."
  {error}
  {onBack}
  actionLabel="+ New agent"
  onAction={() => showCreate = true}
  background="var(--surface)"
>
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
</PageLayout>

<!-- Create agent modal -->
{#if showCreate}
  <Modal title="New agent" onclose={() => showCreate = false}>
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
  </Modal>
{/if}

<!-- Unpublish confirmation modal -->
{#if unpublishName}
  <Modal title="Unpublish agent?" onclose={() => unpublishName = null}>
    <p>The agent <strong>{unpublishName}</strong> will no longer be available as a persona.</p>
    <div class="modal-btns">
      <button class="cancel-btn" onclick={() => unpublishName = null} disabled={unpublishing}>Cancel</button>
      <button class="danger-btn" onclick={() => unpublishAgent(unpublishName)} disabled={unpublishing}>
        {unpublishing ? 'Unpublishing…' : 'Unpublish'}
      </button>
    </div>
  </Modal>
{/if}

<style>
  .agents-table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
  .agents-table th {
    text-align: left; color: var(--text3); font-weight: 500;
    padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--border);
  }
  .agents-table td {
    padding: 0.6rem 0.75rem; border-bottom: 1px solid var(--depth);
    vertical-align: middle; color: var(--text2);
  }

  .slug {
    font-family: monospace; font-size: 0.8rem;
    background: var(--surface-hi); color: var(--text2);
    padding: 0.15rem 0.4rem; border-radius: 3px;
  }

  .badge {
    font-size: 0.75rem; padding: 0.15rem 0.5rem; border-radius: 3px;
    background: var(--surface-hi); color: var(--accent-light); border: 1px solid var(--border);
  }
  .badge.external {
    background: var(--depth); color: var(--accent-light); border-color: var(--accent);
  }
  .badge.proxy {
    background: var(--depth); color: var(--text2); border-color: var(--border);
    margin-left: 0.25rem;
  }

  .actions { display: flex; gap: 0.5rem; white-space: nowrap; }

  .open-btn {
    background: var(--accent); color: var(--bg); border: none;
    border-radius: 4px; padding: 0.2rem 0.6rem;
    cursor: pointer; font-size: 0.8rem; transition: background 0.15s;
  }
  .open-btn:hover { background: var(--accent-light); }

  .unpublish-btn {
    background: none; border: 1px solid var(--danger-border);
    color: var(--danger); border-radius: 4px;
    padding: 0.2rem 0.6rem; cursor: pointer; font-size: 0.8rem;
  }
  .unpublish-btn:hover { background: var(--danger-bg); }

  .visibility-label { gap: 0.5rem; }
  .toggle-row {
    display: flex; gap: 0; border-radius: 6px; overflow: hidden;
    border: 1px solid var(--border); width: fit-content;
  }
  .toggle-btn {
    background: var(--surface-hi); border: none; color: var(--text3);
    padding: 0.4rem 1rem; cursor: pointer; font-size: 0.85rem;
    transition: background 0.15s, color 0.15s;
  }
  .toggle-btn.active { background: var(--accent); color: var(--bg); }
  .toggle-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
