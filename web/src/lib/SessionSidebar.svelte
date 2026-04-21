<script>
  let { sessions, activeId, user, showAll = $bindable(false), collapsed = $bindable(false), searchQuery = $bindable(''), onSelect, onNew, onDelete, onAccount, onApiKeys, onAgents } = $props()

  let confirmId   = $state(null)  // session ID pending delete confirmation
  let userMenuOpen = $state(false)

  function label(sess) {
    const date = sess.last_active_at || sess.created_at
    const ago = relativeTime(new Date(date))
    const persona = sess.persona && sess.persona !== 'default' ? ` · ${sess.persona}` : ''
    const source = showAll && sess.source && sess.source !== 'web' ? ` [${sess.source}]` : ''
    return `${ago}${persona}${source}`
  }

  function relativeTime(date) {
    const diff = Math.floor((Date.now() - date) / 1000)
    if (diff <  60)   return 'just now'
    if (diff <  3600) return `${Math.floor(diff / 60)}m ago`
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
    return `${Math.floor(diff / 86400)}d ago`
  }

  function requestDelete(e, id) {
    e.stopPropagation()
    confirmId = id
  }

  function cancelDelete() {
    confirmId = null
  }

  async function confirmDelete() {
    const id = confirmId
    confirmId = null
    await onDelete?.(id)
  }

  function toggleUserMenu(e) {
    e.stopPropagation()
    userMenuOpen = !userMenuOpen
  }

  function closeUserMenu() {
    userMenuOpen = false
  }
</script>

<style>
  .sidebar {
    width: 200px;
    flex-shrink: 0;
    background: #13161f;
    border-right: 1px solid #2d3148;
    display: flex;
    flex-direction: column;
    overflow: visible;
    transition: width 0.2s ease;
  }
  .sidebar.collapsed {
    width: 36px;
  }


  /* ── Toggle button ── */
  .collapse-btn {
    background: none;
    border: none;
    color: #475569;
    cursor: pointer;
    font-size: 0.75rem;
    line-height: 1;
    padding: 0.2rem 0.3rem;
    border-radius: 4px;
    transition: color 0.15s, background 0.15s;
    flex-shrink: 0;
  }
  .collapse-btn:hover { color: #94a3b8; background: #1e2235; }

  /* ── Header ── */
  .app-title {
    font-size: 1rem;
    font-weight: 700;
    color: #e2e8f0;
    letter-spacing: 0.01em;
    padding-bottom: 0.3rem;
    border-bottom: 1px solid #2d3148;
    margin-bottom: 0.1rem;
  }
  .sidebar-header {
    padding: 0.65rem 0.75rem;
    border-bottom: 1px solid #2d3148;
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: 0.45rem;
  }
  .sidebar.collapsed .sidebar-header {
    padding: 0.65rem 0.3rem;
    align-items: center;
  }

  .header-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .sidebar-title {
    font-size: 0.72rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: #475569;
  }

  button.new-btn {
    background: none;
    border: 1px solid #2d3148;
    border-radius: 5px;
    color: #94a3b8;
    font-size: 0.75rem;
    padding: 0.2rem 0.5rem;
    cursor: pointer;
    transition: background 0.15s, color 0.15s;
  }
  button.new-btn:hover { background: #1e2235; color: #e2e8f0; }

  /* ── Filter toggle ── */
  .filter-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.72rem;
    color: #475569;
    cursor: pointer;
    user-select: none;
  }
  .filter-row input[type=checkbox] {
    accent-color: #6366f1;
    width: 11px;
    height: 11px;
    cursor: pointer;
  }

  /* ── Search ── */
  .search-input {
    width: 100%;
    background: #0f1117;
    border: 1px solid #2d3148;
    border-radius: 5px;
    color: #94a3b8;
    font-size: 0.75rem;
    padding: 0.3rem 0.5rem;
    outline: none;
    transition: border-color 0.15s;
  }
  .search-input::placeholder { color: #334155; }
  .search-input:focus { border-color: #6366f1; }

  /* ── List ── */
  .session-list {
    flex: 1;
    overflow-y: auto;
    padding: 0.35rem 0;
  }

  .session-item {
    padding: 0.45rem 0.75rem;
    cursor: pointer;
    border-left: 2px solid transparent;
    transition: background 0.1s;
    display: flex;
    align-items: flex-start;
    gap: 0.3rem;
  }
  .session-item:hover { background: #1a1d27; }
  .session-item.active {
    background: #1e2235;
    border-left-color: #6366f1;
  }

  .sess-body { flex: 1; min-width: 0; }

  .sess-id {
    font-size: 0.78rem;
    color: #94a3b8;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sess-id.untitled {
    font-family: monospace;
    font-size: 0.72rem;
    color: #475569;
  }
  .session-item.active .sess-id { color: #a5b4fc; }

  .sess-meta {
    font-size: 0.7rem;
    color: #475569;
    margin-top: 0.1rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .sess-msgs {
    font-size: 0.68rem;
    color: #334155;
  }

  /* ── Delete button ── */
  button.delete-btn {
    background: none;
    border: none;
    color: #334155;
    font-size: 0.8rem;
    line-height: 1;
    padding: 0.1rem 0.2rem;
    cursor: pointer;
    border-radius: 3px;
    flex-shrink: 0;
    transition: color 0.15s, background 0.15s;
    visibility: hidden;
  }
  .session-item:hover button.delete-btn { visibility: visible; }
  button.delete-btn:hover { color: #fca5a5; background: #3b1515; }

  /* ── User footer ── */
  .user-footer {
    border-top: 1px solid #2d3148;
    flex-shrink: 0;
    position: relative;
    margin-top: auto;
  }

  .user-btn {
    width: 100%;
    background: none;
    border: none;
    color: #64748b;
    font-size: 0.78rem;
    padding: 0.6rem 0.75rem;
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    text-align: left;
    transition: background 0.15s, color 0.15s;
  }
  .user-btn:hover { background: #1a1d27; color: #94a3b8; }
  .user-avatar {
    width: 22px;
    height: 22px;
    border-radius: 50%;
    background: #2d3148;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.65rem;
    color: #94a3b8;
    flex-shrink: 0;
  }
  .user-menu {
    position: absolute;
    bottom: 0;
    left: 100%;
    margin-left: 6px;
    min-width: 180px;
    background: #1a1d27;
    border: 1px solid #2d3148;
    border-radius: 8px;
    overflow: hidden;
    z-index: 200;
    box-shadow: 0 4px 12px rgba(0,0,0,0.4);
  }
  .menu-username {
    padding: 0.55rem 0.75rem 0.3rem;
    font-size: 0.75rem;
    color: #64748b;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    border-bottom: 1px solid #2d3148;
    margin-bottom: 0.2rem;
  }
  .menu-item {
    display: block;
    width: 100%;
    padding: 0.55rem 0.75rem;
    font-size: 0.8rem;
    color: #94a3b8;
    text-decoration: none;
    background: none;
    border: none;
    cursor: pointer;
    text-align: left;
    transition: background 0.1s, color 0.1s;
  }
  .menu-item:hover { background: #242840; color: #e2e8f0; }

  /* ── Confirm overlay ── */
  .confirm-overlay {
    position: absolute;
    inset: 0;
    background: rgba(15,17,23,.85);
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.75rem;
    padding: 1rem;
    z-index: 10;
  }
  .confirm-msg {
    font-size: 0.8rem;
    color: #94a3b8;
    text-align: center;
    line-height: 1.5;
  }
  .confirm-btns { display: flex; gap: 0.5rem; }
  button.conf-cancel {
    padding: 0.35rem 0.8rem;
    border-radius: 5px;
    font-size: 0.8rem;
    cursor: pointer;
    border: 1px solid #2d3148;
    background: none;
    color: #94a3b8;
  }
  button.conf-cancel:hover { background: #1e2235; }
  button.conf-delete {
    padding: 0.35rem 0.8rem;
    border-radius: 5px;
    font-size: 0.8rem;
    cursor: pointer;
    border: none;
    background: #7f1d1d;
    color: #fca5a5;
  }
  button.conf-delete:hover { background: #991b1b; }

  /* ── Collapsed state ── */
  .sidebar.collapsed .user-footer {
    justify-content: center;
    padding: 0.5rem 0;
  }
  .sidebar.collapsed .user-btn {
    justify-content: center;
    padding: 0.4rem;
  }
</style>

<!-- Click-outside to close user menu -->
<svelte:window onclick={closeUserMenu} />

<div class="sidebar" class:collapsed style="position:relative">
  <div class="sidebar-header">
    {#if !collapsed}
      <div class="app-title">Stoa</div>
    {/if}
    <div class="header-row">
      {#if !collapsed}
        <span class="sidebar-title">Sessions</span>
      {/if}
      <button class="collapse-btn" onclick={() => collapsed = !collapsed}
        title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}>
        {collapsed ? '▶' : '◀'}
      </button>
      {#if !collapsed}
        <button class="new-btn" onclick={onNew}>+ New</button>
      {/if}
    </div>
    {#if !collapsed}
      <label class="filter-row">
        <input type="checkbox" bind:checked={showAll} />
        Other sources
      </label>
      <input
        class="search-input"
        type="search"
        placeholder="Search…"
        bind:value={searchQuery}
      />
    {/if}
  </div>

  {#if !collapsed}
    <div class="session-list">
      {#each sessions as sess (sess.id)}
        <div
          class="session-item"
          class:active={sess.id === activeId}
          onclick={() => onSelect(sess.id)}
          role="button"
          tabindex="0"
          onkeydown={e => e.key === 'Enter' && onSelect(sess.id)}
        >
          <div class="sess-body">
            <div class="sess-id" class:untitled={!sess.title}>
              {sess.title || sess.id.slice(0, 8)}
            </div>
            <div class="sess-meta">{label(sess)}</div>
            {#if sess.message_count > 0}
              <div class="sess-msgs">{sess.message_count} msg{sess.message_count !== 1 ? 's' : ''}</div>
            {/if}
          </div>
          <button
            class="delete-btn"
            onclick={e => requestDelete(e, sess.id)}
            title="Delete session"
            aria-label="Delete session"
          >✕</button>
        </div>
      {/each}
    </div>
  {/if}

  <!-- User footer -->
  {#if user}
    <div class="user-footer">
      {#if userMenuOpen}
        <div class="user-menu">
          <div class="menu-username">{user.email ?? user.name ?? ''}</div>
          <button class="menu-item" onclick={() => { userMenuOpen = false; onAccount?.() }}>Account</button>
          <button class="menu-item" onclick={() => { userMenuOpen = false; onApiKeys?.() }}>API Keys</button>
          <button class="menu-item" onclick={() => { userMenuOpen = false; onAgents?.() }}>Agents</button>
          <button class="menu-item" onclick={() => { fetch('/auth/logout', { method: 'POST' }).then(() => { window.location.href = '/' }) }}>Sign out</button>
        </div>
      {/if}
      <button class="user-btn" onclick={toggleUserMenu}>
        <div class="user-avatar">{(user.email ?? user.name ?? '?')[0].toUpperCase()}</div>
      </button>
    </div>
  {/if}

  <!-- Delete confirmation overlay -->
  {#if confirmId}
    <div class="confirm-overlay">
      <p class="confirm-msg">Delete this conversation?<br>This cannot be undone.</p>
      <div class="confirm-btns">
        <button class="conf-cancel" onclick={cancelDelete}>Cancel</button>
        <button class="conf-delete" onclick={confirmDelete}>Delete</button>
      </div>
    </div>
  {/if}
</div>
