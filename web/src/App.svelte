<script>
  import { onMount, untrack } from 'svelte'
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
  let theme       = $state('dark-stone')

  const POOL_CAP = 5
  let pooledIds = $state([])

  // Keep an LRU pool of mounted Chat instances so switching sessions is instant.
  // Moving to front on each activation; evicting the tail when over cap.
  $effect(() => {
    if (!activeId) return
    const current = untrack(() => pooledIds)
    pooledIds = [activeId, ...current.filter(id => id !== activeId)].slice(0, POOL_CAP)
  })

  onMount(async () => {
    const saved = localStorage.getItem('stoa-theme')
    if (saved) applyTheme(saved)

    try {
      const res = await fetch('/api/me')
      if (res.ok) {
        user = await res.json()
        await loadSessions()
        if (sessions.length === 0) await createSession()
        else activeId = sessions[0].id
      }
    } catch (_) {}
    loading = false
  })

  function applyTheme(t) {
    theme = t
    document.documentElement.dataset.theme = t
    localStorage.setItem('stoa-theme', t)
    // Update color-scheme meta for Dark Reader
    const meta = document.querySelector('meta[name="color-scheme"]')
    if (meta) meta.content = t === 'marble' ? 'light' : 'dark'
  }

  async function loadSessions() {
    try {
      const url = searchQuery.trim()
        ? `/api/sessions?q=${encodeURIComponent(searchQuery.trim())}`
        : '/api/sessions'
      const res = await fetch(url)
      if (res.ok) {
        const all = await res.json() ?? []
        sessions = all.filter(s =>
          showAll || s.source === 'web' || !s.source
        )
      }
    } catch (_) {}
  }

  async function deleteSession(id) {
    await fetch(`/api/sessions/${id}`, { method: 'DELETE' })
    pooledIds = pooledIds.filter(pid => pid !== id)
    if (id === activeId) activeId = null
    await loadSessions()
    if (!activeId && sessions.length > 0) activeId = sessions[0].id
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

  $effect(() => {
    if (!user) return
    void showAll
    void searchQuery
    loadSessions()
    const interval = setInterval(loadSessions, 10_000)
    return () => clearInterval(interval)
  })
</script>

<style>
  :global(:root) {
    --bg:           #0a0908;
    --sidebar:      #131211;
    --surface:      #1c1a18;
    --surface-hi:   #242220;
    --depth:        #070605;
    --hover:        #2a2825;
    --border:       #2c2a27;
    --text:         #e2ddd7;
    --text2:        #948e87;
    --text3:        #6b6560;
    --text4:        #4a4540;
    --text5:        #302d2a;
    --accent:       #9a8f82;
    --accent-light: #c4bab2;
    --accent-dark:  #7a706a;
    --bubble-user:  #3c3835;
    color-scheme: dark;
    background: #0a0908;
  }

  :global([data-theme="marble"]) {
    --bg:           #e8e4df;
    --sidebar:      #f5f3f0;
    --surface:      #faf8f6;
    --surface-hi:   #edeae6;
    --depth:        #ddd9d3;
    --hover:        #e4e0da;
    --border:       #ccc8c2;
    --text:         #1c1a18;
    --text2:        #5c5750;
    --text3:        #7a7570;
    --text4:        #9a9590;
    --text5:        #b0aba5;
    --accent:       #4a4238;
    --accent-light: #7a6e65;
    --accent-dark:  #2c2520;
    --bubble-user:  #3a3330;
    color-scheme: light;
    background: #e8e4df;
  }

  :global([data-theme="sandstone"]) {
    --bg:           #100c06;
    --sidebar:      #1a1208;
    --surface:      #241a0e;
    --surface-hi:   #2e2214;
    --depth:        #0c0804;
    --hover:        #342818;
    --border:       #3a2e1c;
    --text:         #ecddc8;
    --text2:        #9a8468;
    --text3:        #6e5a40;
    --text4:        #4a3c28;
    --text5:        #302518;
    --accent:       #c08840;
    --accent-light: #e0a860;
    --accent-dark:  #9a6c28;
    --bubble-user:  #5a3e20;
    color-scheme: dark;
    background: #100c06;
  }

  :global([data-theme="slate"]) {
    --bg:           #08090c;
    --sidebar:      #0e1016;
    --surface:      #161a22;
    --surface-hi:   #1e2430;
    --depth:        #050608;
    --hover:        #202838;
    --border:       #222830;
    --text:         #d8dde4;
    --text2:        #7a8490;
    --text3:        #556070;
    --text4:        #3a4450;
    --text5:        #252d38;
    --accent:       #5a7898;
    --accent-light: #8aaac8;
    --accent-dark:  #3a5878;
    --bubble-user:  #1e3a58;
    color-scheme: dark;
    background: #08090c;
  }

  :global(*, *::before, *::after) { box-sizing: border-box; margin: 0; padding: 0; }
  :global(body) {
    font-family: system-ui, -apple-system, sans-serif;
    font-size: 14px;
    background: var(--bg);
    color: var(--text);
    height: 100dvh;
    overflow: hidden;
  }

  .center {
    display: flex; align-items: center; justify-content: center; height: 100dvh;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2.5rem;
    text-align: center;
    width: 320px;
  }
  h1 { font-size: 1.4rem; font-weight: 600; margin-bottom: 0.5rem; color: var(--text); }
  p  { color: var(--text3); font-size: 0.9rem; margin-bottom: 1.75rem; }
  a.btn {
    display: block; padding: 0.65rem; background: var(--accent); color: var(--bg);
    border-radius: 6px; text-decoration: none; font-weight: 500;
    transition: background 0.15s;
  }
  a.btn:hover { background: var(--accent-light); }

  .spinner {
    width: 32px; height: 32px; border: 3px solid var(--border);
    border-top-color: var(--accent); border-radius: 50%;
    animation: spin 0.7s linear infinite; margin: 0 auto;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  .app-shell { display: flex; height: 100dvh; overflow: hidden; }
  .chat-slot { display: contents; }
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
      {theme}
      bind:showAll
      bind:collapsed={sidebarCollapsed}
      bind:searchQuery
      onSelect={selectSession}
      onNew={createSession}
      onDelete={deleteSession}
      onAccount={() => { showAccount = true; showApiKeys = false; showAgents = false }}
      onApiKeys={() => { showApiKeys = true; showAccount = false; showAgents = false }}
      onAgents={() => { showAgents = true; showAccount = false; showApiKeys = false }}
      onThemeChange={applyTheme}
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
    {/if}

    {#each pooledIds as id (id)}
      {@const chatActive = !showAccount && !showApiKeys && !showAgents && id === activeId}
      <div class="chat-slot" style={chatActive ? '' : 'display: none'}>
        <Chat
          {user}
          sessionId={id}
          sessionTitle={sessions.find(s => s.id === id)?.title ?? ''}
          onSessionEnd={loadSessions}
          onTitleUpdate={loadSessions}
          active={chatActive}
        />
      </div>
    {/each}
  </div>
{/if}
