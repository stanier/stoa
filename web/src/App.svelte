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
  let settingsOpen = $state(false)
  let toolsOpen    = $state(false)

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
    --bg:           #0e0d0c;
    --sidebar:      #111110;
    --surface:      #1c1b19;
    --surface-hi:   #262421;
    --depth:        #0a0908;
    --hover:        #2a2824;
    --border:       #3a3733;
    --text:         #ece7e0;
    --text2:        #b0a89f;
    --text3:        #857d75;
    --text4:        #5e5852;
    --text5:        #3a3632;
    --accent:       #a89a8a;
    --accent-light: #d4c8bc;
    --accent-dark:  #8a7e72;
    --bubble-user:  #3c3835;
    color-scheme: dark;
    background: #0e0d0c;
  }

  :global([data-theme="marble"]) {
    --bg:           #f4f2ef;
    --sidebar:      #eceae6;
    --surface:      #f9f7f5;
    --surface-hi:   #eae7e3;
    --depth:        #e0ddd8;
    --hover:        #e2dfda;
    --border:       #c4bfb8;
    --text:         #14110f;
    --text2:        #3d3832;
    --text3:        #5e574f;
    --text4:        #7d756c;
    --text5:        #a09a92;
    --accent:       #3a322a;
    --accent-light: #6a5e54;
    --accent-dark:  #1c1612;
    --bubble-user:  #3a3330;
    color-scheme: light;
    background: #f4f2ef;
  }

  :global([data-theme="sandstone"]) {
    --bg:           #18120a;
    --sidebar:      #1c160d;
    --surface:      #251c12;
    --surface-hi:   #322618;
    --depth:        #100c06;
    --hover:        #36291a;
    --border:       #4a3a24;
    --text:         #f4e6d0;
    --text2:        #b89c78;
    --text3:        #8a7250;
    --text4:        #5e4a32;
    --text5:        #3a2e1d;
    --accent:       #d49858;
    --accent-light: #ecbc7c;
    --accent-dark:  #a87838;
    --bubble-user:  #5a3e20;
    color-scheme: dark;
    background: #18120a;
  }

  :global([data-theme="slate"]) {
    --bg:           #0b0d10;
    --sidebar:      #0e1114;
    --surface:      #171c25;
    --surface-hi:   #1f2632;
    --depth:        #08090c;
    --hover:        #242c3a;
    --border:       #2c3440;
    --text:         #e4e8ee;
    --text2:        #94a0ae;
    --text3:        #6a7585;
    --text4:        #48525e;
    --text5:        #2c343f;
    --accent:       #7090b0;
    --accent-light: #a0bcd8;
    --accent-dark:  #4a6888;
    --bubble-user:  #1e3a58;
    color-scheme: dark;
    background: #0b0d10;
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
          bind:settingsOpen
          bind:toolsOpen
        />
      </div>
    {/each}
  </div>
{/if}
