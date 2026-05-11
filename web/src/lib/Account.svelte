<script>
  import { onMount } from 'svelte'
  import UsagePanel from './UsagePanel.svelte'
  import McpServerList from './McpServerList.svelte'

  let { onBack } = $props()

  let account    = $state(null)
  let error      = $state(null)
  let loading    = $state(true)

  // MCP tool prefs state
  let allTools      = $state([])    // [{ name, description, source }]
  let prefs         = $state({ servers: {}, tools: {} })
  let toolsError    = $state(null)
  let toolsLoading  = $state(true)
  let saving        = $state(false)

  onMount(async () => {
    try {
      const res = await fetch('/api/account')
      if (res.ok) {
        account = await res.json()
      } else {
        error = 'Failed to load account info.'
      }
    } catch (_) {
      error = 'Network error.'
    }
    loading = false

    // Load tools + prefs independently (don't block the profile card).
    await loadToolPrefs()
  })

  async function loadToolPrefs() {
    toolsLoading = true
    toolsError = null
    try {
      const [toolsRes, prefsRes] = await Promise.all([
        fetch('/api/tools'),
        fetch('/api/mcp-prefs'),
      ])
      if (toolsRes.ok) {
        allTools = await toolsRes.json()
      } else {
        toolsError = `Could not load tools (${toolsRes.status}).`
      }
      if (prefsRes.ok) {
        const data = await prefsRes.json()
        prefs = {
          servers: data.servers ?? {},
          tools:   data.tools   ?? {},
        }
      }
    } catch (e) {
      toolsError = 'Could not load tool preferences.'
    } finally {
      toolsLoading = false
    }
  }

  function serverEnabled(serverName) {
    return prefs.servers[serverName] !== false
  }

  function toolEnabled(toolName) {
    if (toolName in prefs.tools) return prefs.tools[toolName]
    const server = allTools.find(t => t.name === toolName)?.source.replace(/^mcp_/, '')
    return serverEnabled(server)
  }

  async function toggleServer(serverName, enabled) {
    saving = true
    try {
      await fetch(`/api/mcp-prefs/servers/${encodeURIComponent(serverName)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled }),
      })
      prefs = { ...prefs, servers: { ...prefs.servers, [serverName]: enabled } }
    } catch (_) {}
    saving = false
  }

  async function toggleTool(toolName, enabled) {
    saving = true
    try {
      await fetch(`/api/mcp-prefs/tools/${encodeURIComponent(toolName)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled }),
      })
      prefs = { ...prefs, tools: { ...prefs.tools, [toolName]: enabled } }
    } catch (_) {}
    saving = false
  }
</script>

<style>
  .page {
    flex: 1;
    overflow-y: auto;
    padding: 2rem;
    background: var(--bg);
    color: var(--text);
  }

  .back-btn {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.4rem 0.75rem;
    background: none;
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text2);
    font-size: 0.85rem;
    cursor: pointer;
    transition: background 0.15s, color 0.15s;
    margin-bottom: 2rem;
  }
  .back-btn:hover { background: var(--surface); color: var(--text); }

  h1 {
    font-size: 1.4rem;
    font-weight: 600;
    margin-bottom: 2rem;
    color: var(--text);
  }

  .card {
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
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .saving-label {
    font-size: 0.7rem;
    color: var(--accent);
    font-weight: normal;
    text-transform: none;
    letter-spacing: 0;
  }

  .card-note {
    font-size: 0.78rem;
    color: var(--text3);
    margin-bottom: 1rem;
    line-height: 1.5;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    margin-bottom: 0.85rem;
  }
  .field:last-child { margin-bottom: 0; }

  .field-label { font-size: 0.75rem; color: var(--text3); }
  .field-value { font-size: 0.95rem; color: var(--text); word-break: break-all; }

  .link-badge {
    display: inline-block;
    padding: 0.25rem 0.6rem;
    background: var(--surface-hi);
    border: 1px solid var(--border);
    border-radius: 4px;
    font-size: 0.82rem;
    color: var(--text2);
    margin: 0.2rem 0.2rem 0.2rem 0;
    font-family: monospace;
  }

  .no-links { font-size: 0.85rem; color: var(--text3); font-style: italic; }

  .spinner {
    width: 24px; height: 24px;
    border: 2px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
    margin: 3rem auto;
  }
  .error { color: var(--danger); font-size: 0.9rem; margin-top: 1rem; }

  .no-tools { font-size: 0.85rem; color: var(--text3); font-style: italic; }

  .tools-spinner {
    width: 18px; height: 18px;
    border: 2px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
    margin: 0.5rem 0;
  }
</style>

<div class="page">
  <button class="back-btn" onclick={onBack}>← Back</button>

  <h1>Account</h1>

  {#if loading}
    <div class="spinner"></div>

  {:else if error}
    <p class="error">{error}</p>

  {:else if account}
    <div class="card">
      <div class="card-title">Profile</div>
      {#if account.name}
        <div class="field">
          <span class="field-label">Name</span>
          <span class="field-value">{account.name}</span>
        </div>
      {/if}
      <div class="field">
        <span class="field-label">Email</span>
        <span class="field-value">{account.email}</span>
      </div>
      <div class="field">
        <span class="field-label">User ID</span>
        <span class="field-value">{account.user_id}</span>
      </div>
      {#if account.created_at}
        <div class="field">
          <span class="field-label">Member since</span>
          <span class="field-value">{new Date(account.created_at).toLocaleDateString(undefined, { dateStyle: 'medium' })}</span>
        </div>
      {/if}
    </div>

    <div class="card">
      <div class="card-title">Linked services</div>
      <div class="field">
        <span class="field-label">Zulip</span>
        {#if account.links?.zulip?.length > 0}
          <div>
            {#each account.links.zulip as email}
              <span class="link-badge">{email}</span>
            {/each}
          </div>
        {:else}
          <span class="no-links">No Zulip account linked</span>
        {/if}
      </div>
    </div>

    <!-- Token usage summary -->
    <UsagePanel />

    <!-- MCP tool preferences -->
    <div class="card">
      <div class="card-title">
        Tools
        {#if saving}<span class="saving-label">Saving…</span>{/if}
      </div>
      <p class="card-note">
        These defaults apply to every new session.
        You can override them per session from the chat toolbar.
      </p>

      {#if toolsLoading}
        <div class="tools-spinner"></div>
      {:else if toolsError}
        <p class="error">{toolsError}</p>
      {:else if allTools.filter(t => t.source !== 'builtin').length === 0}
        <span class="no-tools">No MCP servers configured</span>
      {:else}
        <McpServerList
          {allTools}
          {saving}
          {serverEnabled}
          {toolEnabled}
          onToggleServer={toggleServer}
          onToggleTool={toggleTool}
        />
      {/if}
    </div>
  {/if}
</div>
