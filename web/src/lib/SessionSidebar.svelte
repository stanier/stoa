<script>
  let { sessions, activeId, user, theme = 'dark-stone', showAll = $bindable(false), collapsed = $bindable(false), searchQuery = $bindable(''), onSelect, onNew, onDelete, onAccount, onApiKeys, onAgents, onThemeChange } = $props()

  let confirmId    = $state(null)
  let userMenuOpen = $state(false)
  let sidebarWidth = $state(200)
  let isResizing   = $state(false)
  let pillHovered  = $state(false)

  const themes = [
    { id: 'dark-stone', label: 'Dark Stone' },
    { id: 'marble',     label: 'Marble' },
    { id: 'sandstone',  label: 'Sandstone' },
    { id: 'slate',      label: 'Slate' },
  ]

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

  function cancelDelete() { confirmId = null }

  async function confirmDelete() {
    const id = confirmId
    confirmId = null
    await onDelete?.(id)
  }

  function toggleUserMenu(e) {
    e.stopPropagation()
    userMenuOpen = !userMenuOpen
  }

  function closeUserMenu() { userMenuOpen = false }

  function onPillMousedown(e) {
    e.preventDefault()
    const anchorX     = e.clientX
    const anchorWidth = sidebarWidth
    let   hasDragged  = false

    function onMousemove(e) {
      if (!hasDragged) {
        if (Math.abs(e.clientX - anchorX) > 4 && !collapsed) {
          hasDragged = true
          isResizing = true
          document.body.style.cursor     = 'col-resize'
          document.body.style.userSelect = 'none'
        }
        return
      }
      sidebarWidth = Math.max(140, Math.min(400, anchorWidth + (e.clientX - anchorX)))
    }

    function onMouseup() {
      document.removeEventListener('mousemove', onMousemove)
      document.removeEventListener('mouseup',   onMouseup)
      document.body.style.cursor     = ''
      document.body.style.userSelect = ''
      isResizing = false
      if (!hasDragged) {
        collapsed = !collapsed
      }
    }

    document.addEventListener('mousemove', onMousemove)
    document.addEventListener('mouseup',   onMouseup)
  }
</script>

<style>
  .sidebar {
    flex-shrink: 0;
    background: var(--sidebar);
    display: flex;
    flex-direction: column;
    overflow: visible;
    position: relative;
  }

  /* Single content wrapper — header + session list fade together */
  .sidebar-content {
    flex: 1;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .sidebar-content.visible {
    opacity: 1;
    pointer-events: auto;
    transition: opacity 0.18s ease 0.1s;
  }
  .sidebar-content.hidden {
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.08s ease;
  }

  .sidebar-header {
    padding: 0.65rem 0.75rem;
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: 0.45rem;
  }

  .app-title {
    font-size: 1rem;
    font-weight: 700;
    color: var(--text);
    letter-spacing: 0.01em;
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
    color: var(--text4);
  }

  button.new-btn {
    background: none;
    border: 1px solid var(--border);
    border-radius: 5px;
    color: var(--text2);
    font-size: 0.75rem;
    padding: 0.2rem 0.5rem;
    cursor: pointer;
    transition: background 0.15s, color 0.15s;
  }
  button.new-btn:hover { background: var(--hover); color: var(--text); }

  .search-input {
    width: 100%;
    background: var(--depth);
    border: 1px solid var(--border);
    border-radius: 5px;
    color: var(--text2);
    font-size: 0.75rem;
    padding: 0.3rem 0.5rem;
    outline: none;
    transition: border-color 0.15s;
  }
  .search-input::placeholder { color: var(--text5); }
  .search-input:focus { border-color: var(--accent); }

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
  .session-item:hover { background: var(--surface); }
  .session-item.active {
    background: var(--surface-hi);
    border-left-color: var(--accent);
  }

  .sess-body { flex: 1; min-width: 0; }

  .sess-id {
    font-size: 0.78rem;
    color: var(--text2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sess-id.untitled {
    font-family: monospace;
    font-size: 0.72rem;
    color: var(--text4);
  }
  .session-item.active .sess-id { color: var(--accent-light); }

  .sess-meta {
    font-size: 0.7rem;
    color: var(--text4);
    margin-top: 0.1rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .sess-msgs {
    font-size: 0.68rem;
    color: var(--text5);
  }

  button.delete-btn {
    background: none;
    border: none;
    color: var(--text5);
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

  /* Footer */
  .user-footer {
    flex-shrink: 0;
    position: relative;
  }

  .user-btn {
    width: 100%;
    background: none;
    border: none;
    color: var(--text3);
    font-size: 0.78rem;
    padding: 0.6rem 0.75rem;
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    text-align: left;
    transition: background 0.15s, color 0.15s;
    overflow: hidden;
  }
  .user-btn:hover { background: var(--surface); color: var(--text2); }

  .user-avatar {
    width: 22px;
    height: 22px;
    border-radius: 50%;
    background: var(--surface-hi);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.65rem;
    color: var(--text2);
    flex-shrink: 0;
  }

  /* Username label collapses via max-width + opacity */
  .username-label {
    font-size: 0.75rem;
    color: var(--text3);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .username-label.visible {
    max-width: 200px;
    opacity: 1;
    transition: opacity 0.18s ease 0.1s, max-width 0.2s ease;
  }
  .username-label.hidden {
    max-width: 0;
    opacity: 0;
    transition: opacity 0.08s ease, max-width 0.2s ease;
  }

  /* Collapsed footer: center the avatar */
  .user-btn.centered {
    justify-content: center;
    padding: 0.6rem 0;
  }

  .user-menu {
    position: absolute;
    bottom: 0;
    left: 100%;
    margin-left: 6px;
    min-width: 200px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
    z-index: 200;
    box-shadow: 0 4px 12px rgba(0,0,0,0.4);
  }
  .menu-username {
    padding: 0.55rem 0.75rem 0.3rem;
    font-size: 0.75rem;
    color: var(--text3);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    border-bottom: 1px solid var(--border);
    margin-bottom: 0.2rem;
  }
  .menu-item {
    display: block;
    width: 100%;
    padding: 0.55rem 0.75rem;
    font-size: 0.8rem;
    color: var(--text2);
    text-decoration: none;
    background: none;
    border: none;
    cursor: pointer;
    text-align: left;
    transition: background 0.1s, color 0.1s;
  }
  .menu-item:hover { background: var(--hover); color: var(--text); }

  .menu-toggle-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.45rem 0.75rem;
    font-size: 0.8rem;
    color: var(--text2);
  }

  .menu-section-label {
    padding: 0.4rem 0.75rem 0.2rem;
    font-size: 0.68rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text4);
    border-top: 1px solid var(--border);
    margin-top: 0.2rem;
  }

  .theme-options {
    display: flex;
    gap: 0.3rem;
    padding: 0.3rem 0.75rem 0.5rem;
    flex-wrap: wrap;
  }
  .theme-btn {
    font-size: 0.72rem;
    padding: 0.2rem 0.5rem;
    border-radius: 4px;
    border: 1px solid var(--border);
    background: none;
    color: var(--text3);
    cursor: pointer;
    transition: background 0.15s, color 0.15s, border-color 0.15s;
  }
  .theme-btn:hover { color: var(--text); border-color: var(--accent); }
  .theme-btn.active { background: var(--surface-hi); color: var(--accent-light); border-color: var(--accent); }

  /* Toggle switch (small, for menu) */
  .mini-switch { position: relative; width: 28px; height: 16px; flex-shrink: 0; }
  .mini-switch input { opacity: 0; width: 0; height: 0; }
  .mini-slider {
    position: absolute; inset: 0; background: var(--border);
    border-radius: 16px; cursor: pointer; transition: background 0.2s;
  }
  .mini-slider::before {
    content: ''; position: absolute; width: 10px; height: 10px;
    left: 3px; top: 3px; background: var(--text3); border-radius: 50%;
    transition: transform 0.2s, background 0.2s;
  }
  input:checked + .mini-slider { background: var(--accent); }
  input:checked + .mini-slider::before { transform: translateX(12px); background: var(--bg); }

  .confirm-overlay {
    position: absolute;
    inset: 0;
    background: color-mix(in srgb, var(--bg) 85%, transparent);
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
    color: var(--text2);
    text-align: center;
    line-height: 1.5;
  }
  .confirm-btns { display: flex; gap: 0.5rem; }
  button.conf-cancel {
    padding: 0.35rem 0.8rem;
    border-radius: 5px;
    font-size: 0.8rem;
    cursor: pointer;
    border: 1px solid var(--border);
    background: none;
    color: var(--text2);
  }
  button.conf-cancel:hover { background: var(--surface-hi); }
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

  /* Edge pill handle */
  .pill-handle {
    position: absolute;
    right: -8px;
    top: 50%;
    transform: translateY(-50%);
    width: 16px;
    height: 48px;
    z-index: 50;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: col-resize;
  }

  .pill {
    width: 4px;
    border-radius: 9999px;
    pointer-events: none;
  }
  .pill.resting {
    height: 24px;
    background: var(--border);
    transition: height 0.15s ease, background 0.15s ease;
  }
  .pill.active {
    height: 36px;
    background: var(--accent);
    transition: height 0.15s ease, background 0.15s ease;
  }
  .pill.dragging {
    height: 36px;
    background: var(--accent);
    transition: none;
  }
</style>

<svelte:window onclick={closeUserMenu} />

<div
  class="sidebar"
  style="width: {collapsed ? '36px' : sidebarWidth + 'px'}; transition: {isResizing ? 'none' : 'width 0.2s ease'};"
>
  <!-- Single content wrapper: header + session list fade together -->
  <div class="sidebar-content" class:visible={!collapsed} class:hidden={collapsed}>
    <div class="sidebar-header">
      <div class="app-title">Stoa</div>
      <div class="header-row">
        <span class="sidebar-title">Sessions</span>
        <button class="new-btn" onclick={onNew}>+ New</button>
      </div>
      <input
        class="search-input"
        type="search"
        placeholder="Search…"
        bind:value={searchQuery}
      />
    </div>

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
  </div>

  {#if user}
    <div class="user-footer">
      {#if userMenuOpen}
        <div class="user-menu">
          <div class="menu-username">{user.email ?? user.name ?? ''}</div>
          <button class="menu-item" onclick={() => { userMenuOpen = false; onAccount?.() }}>Account</button>
          <button class="menu-item" onclick={() => { userMenuOpen = false; onApiKeys?.() }}>API Keys</button>
          <button class="menu-item" onclick={() => { userMenuOpen = false; onAgents?.() }}>Agents</button>
          <div class="menu-toggle-row">
            <span>Other sources</span>
            <label class="mini-switch" title="Show sessions from all sources">
              <input type="checkbox" bind:checked={showAll} />
              <span class="mini-slider"></span>
            </label>
          </div>
          <div class="menu-section-label">Theme</div>
          <div class="theme-options">
            {#each themes as t}
              <button
                class="theme-btn"
                class:active={theme === t.id}
                onclick={() => onThemeChange?.(t.id)}
              >{t.label}</button>
            {/each}
          </div>
          <button class="menu-item" onclick={() => { fetch('/auth/logout', { method: 'POST' }).then(() => { window.location.href = '/' }) }}>Sign out</button>
        </div>
      {/if}
      <button class="user-btn" class:centered={collapsed} onclick={toggleUserMenu}>
        <div class="user-avatar">{(user.email ?? user.name ?? '?')[0].toUpperCase()}</div>
        <span class="username-label" class:visible={!collapsed} class:hidden={collapsed}>
          {user.name ?? user.email ?? ''}
        </span>
      </button>
    </div>
  {/if}

  {#if confirmId}
    <div class="confirm-overlay">
      <p class="confirm-msg">Delete this conversation?<br>This cannot be undone.</p>
      <div class="confirm-btns">
        <button class="conf-cancel" onclick={cancelDelete}>Cancel</button>
        <button class="conf-delete" onclick={confirmDelete}>Delete</button>
      </div>
    </div>
  {/if}

  <!-- Edge pill handle: click to toggle, drag to resize -->
  <div
    class="pill-handle"
    onmousedown={onPillMousedown}
    onmouseenter={() => pillHovered = true}
    onmouseleave={() => pillHovered = false}
    role="button"
    tabindex="0"
    aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
    onkeydown={e => e.key === 'Enter' && (collapsed = !collapsed)}
  >
    <div
      class="pill"
      class:resting={!pillHovered && !isResizing}
      class:active={pillHovered && !isResizing}
      class:dragging={isResizing}
    ></div>
  </div>
</div>
