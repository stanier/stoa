<script>
  import { onMount } from 'svelte'
  import Chat from './lib/Chat.svelte'
  import SessionSidebar from './lib/SessionSidebar.svelte'
  import Account from './lib/Account.svelte'
  import ApiKeys from './lib/ApiKeys.svelte'
  import AgentRegistry from './lib/AgentRegistry.svelte'

  let user      = $state(null)
  let loading   = $state(true)
  let sessions  = $state([])
  let activeId  = $state(null)
  let showAll   = $state(false)
  let sidebarCollapsed = $state(false)
  let showAccount = $state(false)
  let showApiKeys = $state(false)
  let showAgents  = $state(false)
  let searchQuery = $state('')

  onMount(async () => {
    try {
      const res = await fetch('/api/me')
      if (res.ok) {
        user = await res.json()
        await loadSessions()
        // Create an initial session if none exist.
        if (sessions.length === 0) await createSession()
        else activeId = sessions[0].id
      }
    } catch (_) {}
    loading = false
  })

  async function loadSessions() {
    try {
      const url = searchQuery.trim()
        ? `/api/sessions?q=${encodeURIComponent(searchQuery.trim())}`
        : '/api/sessions'
      const res = await fetch(url)
      if (res.ok) {
        const all = await res.json() ?? []
        // agentd already filters to the current user's sessions.
        // We only apply the source filter here for UX (not security).
        sessions = all.filter(s =>
          showAll || s.source === 'web' || !s.source
        )
      }
    } catch (_) {}
  }

  async function deleteSession(id) {
    await fetch(`/api/sessions/${id}`, { method: 'DELETE' })
    // If deleting the active session, clear it and pick another.
    if (id === activeId) {
      activeId = null
    }
    await loadSessions()
    // If no active session after deletion, pick the first available.
    if (!activeId && sessions.length > 0) {
      activeId = sessions[0].id
    }
  }


  async function createSession() {
    const res = await fetch('/api/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ persona: 'default', source: 'web' }),
    })
    if (!res.ok) return
    const data = await res.json()
    await loadSessions()
    activeId = data.session_id
  }

  function selectSession(id) {
    if (id !== activeId) activeId = id
  }

  // Refresh session list periodically to pick up message counts.
  // Also re-runs when showAll changes via the $effect above.
  $effect(() => {
    if (!user) return
    void showAll        // declare dependency so toggling triggers a reload
    void searchQuery    // reload immediately when search changes
    loadSessions()
    const interval = setInterval(loadSessions, 10_000)
    return () => clearInterval(interval)
  })
</script>

<style>
  :global(*, *::before, *::after) { box-sizing: border-box; margin: 0; padding: 0; }
  :global(body) {
    font-family: system-ui, -apple-system, sans-serif;
    background: #0f1117;
    color: #e2e8f0;
    height: 100dvh;
    overflow: hidden;
  }

  .center {
    display: flex; align-items: center; justify-content: center; height: 100dvh;
  }

  .card {
    background: #1a1d27; border: 1px solid #2d3148; border-radius: 12px;
    padding: 2.5rem; text-align: center; width: 320px;
  }
  h1 { font-size: 1.4rem; font-weight: 600; margin-bottom: 0.5rem; }
  p  { color: #64748b; font-size: 0.9rem; margin-bottom: 1.75rem; }
  a.btn {
    display: block; padding: 0.65rem; background: #6366f1; color: #fff;
    border-radius: 6px; text-decoration: none; font-weight: 500;
    transition: background 0.15s;
  }
  a.btn:hover { background: #4f46e5; }

  .spinner {
    width: 32px; height: 32px; border: 3px solid #2d3148;
    border-top-color: #6366f1; border-radius: 50%;
    animation: spin 0.7s linear infinite; margin: 0 auto;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  .app-shell { display: flex; height: 100dvh; overflow: hidden; }
</style>

{#if loading}
  <div class="center"><div class="spinner"></div></div>

{:else if !user}
  <div class="center">
    <div class="card">
      <h1>Stoa</h1>
      <p>Sign in to start chatting</p>
      <a class="btn" href="/auth/login">Sign in</a>
    </div>
  </div>

{:else}
  <div class="app-shell">
    <SessionSidebar
      {sessions}
      {activeId}
      {user}
      bind:showAll
      bind:collapsed={sidebarCollapsed}
      bind:searchQuery
      onSelect={selectSession}
      onNew={createSession}
      onDelete={deleteSession}
      onAccount={() => { showAccount = true; showApiKeys = false; showAgents = false }}
      onApiKeys={() => { showApiKeys = true; showAccount = false; showAgents = false }}
      onAgents={() => { showAgents = true; showAccount = false; showApiKeys = false }}
    />
    {#if showAccount}
      <Account onBack={() => showAccount = false} />
    {:else if showApiKeys}
      <ApiKeys onBack={() => showApiKeys = false} />
    {:else if showAgents}
      <AgentRegistry
        onBack={() => showAgents = false}
        onOpenChat={(sessionId) => { showAgents = false; activeId = sessionId; loadSessions() }}
      />
    {:else if activeId}
      {#key activeId}
        <Chat {user} sessionId={activeId} sessionTitle={sessions.find(s => s.id === activeId)?.title ?? ''} onSessionEnd={loadSessions} />
      {/key}
    {/if}
  </div>
{/if}
