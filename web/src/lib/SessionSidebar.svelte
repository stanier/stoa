<script>
  import ProjectIcon from './ProjectIcon.svelte'
  import { apiFetch } from './utils.js'

  let {
    sessions = [],
    activeId,
    user,
    theme = 'dark-stone',
    showAll        = $bindable(false),
    collapsed      = $bindable(false),
    searchQuery    = $bindable(''),
    projects       = [],
    activeProjectId = null,
    sidebarLayout  = 'sections',
    projectIconStyle = 'color-dot',
    onSelect,
    onNew,
    onDelete,
    onAccount,
    onApiKeys,
    onAgents,
    onThemeChange,
    onSelectProject,
    onNewProject,
    onMoveSession,
  } = $props()

  // ── Existing state ──────────────────────────────────────────────────────────
  let confirmId    = $state(null)
  let userMenuOpen = $state(false)
  let sidebarWidth = $state(200)
  let isResizing   = $state(false)
  let pillHovered  = $state(false)

  // ── Search icon-pill ────────────────────────────────────────────────────────
  let searchHovered  = $state(false)
  let searchFocused  = $state(false)
  let searchInputEl  = $state(null)
  let searchExpanded = $derived(searchHovered || searchFocused || !!searchQuery)

  // ── New-chat icon-pill ──────────────────────────────────────────────────────
  let newChatHovered = $state(false)

  // ── Drag-and-drop ───────────────────────────────────────────────────────────
  let dragOverProjectId = $state(null)

  // ── Context menu ────────────────────────────────────────────────────────────
  let contextMenu        = $state(null) // { sessionId, x, y }
  let contextSubmenuOpen = $state(false)

  // ── Folder expansion (folders layout) ──────────────────────────────────────
  let expandedFolders = $state(new Set())

  // ── Tab state (tabs layout) ─────────────────────────────────────────────────
  let sidebarTab          = $state('chats')
  let expandedTabProjects = $state(new Set())

  const themes = [
    { id: 'dark-stone', label: 'Dark Stone' },
    { id: 'marble',     label: 'Marble' },
    { id: 'sandstone',  label: 'Sandstone' },
    { id: 'slate',      label: 'Slate' },
  ]

  // ── Derived session lists ───────────────────────────────────────────────────
  let looseSessionsList = $derived(
    sessions.filter(s => !s.project_id || !projects.find(p => p.id === s.project_id))
  )

  function getProjectSessions(projectId) {
    return sessions.filter(s => s.project_id === projectId)
  }

  function getSession(id) {
    return sessions.find(s => s.id === id)
  }

  // ── Helpers ─────────────────────────────────────────────────────────────────
  function label(sess) {
    const date = sess.last_active_at || sess.created_at
    const ago  = relativeTime(new Date(date))
    const source = showAll && sess.source && sess.source !== 'web' ? ` [${sess.source}]` : ''
    return `${ago}${source}`
  }

  function relativeTime(date) {
    const diff = Math.floor((Date.now() - date) / 1000)
    if (diff <  60)   return 'just now'
    if (diff <  3600) return `${Math.floor(diff / 60)}m ago`
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
    return `${Math.floor(diff / 86400)}d ago`
  }

  // ── Delete confirm ──────────────────────────────────────────────────────────
  function requestDelete(e, id) {
    e?.stopPropagation()
    closeContextMenu()
    confirmId = id
  }
  function cancelDelete()  { confirmId = null }
  async function confirmDelete() {
    const id = confirmId
    confirmId = null
    await onDelete?.(id)
  }

  // ── User menu ───────────────────────────────────────────────────────────────
  function toggleUserMenu(e) { e.stopPropagation(); userMenuOpen = !userMenuOpen }
  function closeUserMenu()   { userMenuOpen = false }

  // ── Resize pill handle ──────────────────────────────────────────────────────
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
      if (!hasDragged) collapsed = !collapsed
    }

    document.addEventListener('mousemove', onMousemove)
    document.addEventListener('mouseup',   onMouseup)
  }

  // ── Drag-and-drop ───────────────────────────────────────────────────────────
  function onDragStart(e, sessId) {
    e.dataTransfer.setData('text/stoa-session', sessId)
    e.dataTransfer.effectAllowed = 'move'
  }

  function onDragOver(e, projectId) {
    e.preventDefault()
    dragOverProjectId = projectId
  }

  function onDragLeave(_e, projectId) {
    if (dragOverProjectId === projectId) dragOverProjectId = null
  }

  function onDrop(e, projectId) {
    e.preventDefault()
    dragOverProjectId = null
    const sessId = e.dataTransfer.getData('text/stoa-session')
    if (sessId) onMoveSession?.(sessId, projectId)
  }

  // ── Context menu ────────────────────────────────────────────────────────────
  function openContextMenu(e, sessId) {
    e.preventDefault()
    e.stopPropagation()
    contextMenu        = { sessionId: sessId, x: e.clientX, y: e.clientY }
    contextSubmenuOpen = false
  }

  function openContextMenuFromBtn(e, sessId) {
    e.stopPropagation()
    const rect = e.currentTarget.getBoundingClientRect()
    contextMenu        = { sessionId: sessId, x: rect.left - 144, y: rect.bottom + 2 }
    contextSubmenuOpen = false
  }

  function closeContextMenu() {
    contextMenu        = null
    contextSubmenuOpen = false
  }

  // ── Folders layout expansion ─────────────────────────────────────────────────
  function toggleFolder(projectId) {
    const next = new Set(expandedFolders)
    if (next.has(projectId)) next.delete(projectId)
    else next.add(projectId)
    expandedFolders = next
  }

  // ── Tabs layout ─────────────────────────────────────────────────────────────
  function selectTabProject(projectId) {
    if (projectId === activeProjectId) {
      const next = new Set(expandedTabProjects)
      if (next.has(projectId)) next.delete(projectId)
      else next.add(projectId)
      expandedTabProjects = next
    } else {
      onSelectProject?.(projectId)
      const next = new Set(expandedTabProjects)
      next.add(projectId)
      expandedTabProjects = next
    }
  }

  // ── Window handlers ──────────────────────────────────────────────────────────
  function handleWindowClick() {
    if (contextMenu) closeContextMenu()
    if (userMenuOpen) closeUserMenu()
  }

  function handleWindowKeydown(e) {
    if (e.key === 'Escape') { closeContextMenu(); closeUserMenu() }
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

  /* ── Header ─────────────────────────────────────────────────────────────── */
  .sidebar-header {
    padding: 0.65rem 0.75rem 0.45rem;
    flex-shrink: 0;
  }

  .header-top-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .app-title {
    font-size: 1rem;
    font-weight: 700;
    color: var(--text);
    letter-spacing: 0.01em;
  }

  .header-actions {
    display: flex;
    align-items: center;
    gap: 0.15rem;
  }

  /* Icon-pill buttons (search + new chat) */
  .icon-pill {
    display: inline-flex;
    align-items: center;
    height: 24px;
    padding: 0.25rem 0.35rem;
    border-radius: 5px;
    border: 1px solid transparent;
    background: transparent;
    color: var(--text2);
    cursor: pointer;
    transition: background 0.15s ease, color 0.15s ease, border-color 0.15s ease;
    white-space: nowrap;
  }
  .icon-pill.expanded {
    background: var(--bg);
    border-color: var(--border);
    color: var(--text);
    cursor: text;
  }
  .icon-pill.new-pill:hover {
    background: var(--hover);
    border-color: var(--border);
    color: var(--text);
  }

  .pill-icon { flex-shrink: 0; }

  .pill-input {
    border: none;
    outline: none;
    background: transparent;
    color: var(--text);
    font-size: 0.75rem;
    font-family: inherit;
    width: 0;
    max-width: 0;
    opacity: 0;
    margin-left: 0;
    transition: width 0.22s ease, max-width 0.22s ease, opacity 0.18s ease, margin-left 0.22s ease;
  }
  .pill-input::placeholder { color: var(--text5); }
  .pill-input.show {
    width: 110px;
    max-width: 110px;
    opacity: 1;
    margin-left: 0.3rem;
  }

  .pill-clear {
    background: none;
    border: none;
    color: var(--text4);
    font-size: 0.7rem;
    cursor: pointer;
    padding: 0;
    line-height: 1;
    max-width: 0;
    opacity: 0;
    overflow: hidden;
    transition: opacity 0.15s, max-width 0.15s;
    margin-left: 0;
    flex-shrink: 0;
  }
  .pill-clear.show {
    max-width: 16px;
    opacity: 1;
    margin-left: 0.15rem;
  }

  .new-label {
    font-size: 0.75rem;
    white-space: nowrap;
    max-width: 0;
    opacity: 0;
    overflow: hidden;
    margin-left: 0;
    transition: max-width 0.22s ease, opacity 0.18s ease, margin-left 0.22s ease;
  }
  .new-label.show {
    max-width: 120px;
    opacity: 1;
    margin-left: 0.3rem;
  }

  /* ── Session list ────────────────────────────────────────────────────────── */
  .session-list {
    flex: 1;
    overflow-y: auto;
    padding: 0.25rem 0;
  }

  /* ── Section labels ──────────────────────────────────────────────────────── */
  .section-label-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.3rem 0.75rem 0.25rem;
  }

  .section-label {
    font-size: 0.66rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--text4);
  }

  .section-add-btn {
    background: none;
    border: none;
    color: var(--text4);
    font-size: 0.8rem;
    cursor: pointer;
    padding: 0.05rem 0.3rem;
    border-radius: 4px;
    line-height: 1;
    transition: color 0.15s, background 0.15s;
  }
  .section-add-btn:hover { color: var(--text2); background: var(--hover); }

  .section-count {
    font-size: 0.65rem;
    color: var(--text5);
  }

  .section-divider {
    height: 1px;
    background: var(--border);
    margin: 0.5rem 0.75rem;
  }

  /* ── Project rows ────────────────────────────────────────────────────────── */
  .project-row {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0.4rem 0.75rem;
    cursor: pointer;
    border-left: 2px solid transparent;
    transition: background 0.1s;
    user-select: none;
  }
  .project-row:hover       { background: var(--hover); }
  .project-row.active      { background: var(--surface); }
  .project-row.drag-over   { background: var(--surface-hi); }

  .project-name {
    flex: 1;
    font-size: 0.8rem;
    color: var(--text2);
    font-weight: 400;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .project-row.active .project-name { color: var(--text); font-weight: 500; }

  .project-count {
    font-size: 0.68rem;
    color: var(--text4);
    font-variant-numeric: tabular-nums;
    flex-shrink: 0;
  }

  .folder-chevron {
    background: none;
    border: none;
    color: var(--text4);
    font-size: 0.6rem;
    cursor: pointer;
    padding: 0.1rem;
    border-radius: 3px;
    flex-shrink: 0;
    line-height: 1;
    display: inline-flex;
    align-items: center;
    transition: transform 0.15s;
  }
  .folder-chevron.open { transform: rotate(90deg); }

  /* ── Projects empty state ─────────────────────────────────────────────────── */
  .projects-empty {
    margin: 0.35rem 0.75rem;
    padding: 0.65rem 0.75rem;
    border-left: 2px dashed var(--border);
  }
  .projects-empty p {
    font-size: 0.72rem;
    color: var(--text4);
    line-height: 1.45;
    margin-bottom: 0.5rem;
  }
  .new-project-btn {
    border: 1px dashed var(--border);
    border-radius: 5px;
    background: none;
    color: var(--text4);
    font-size: 0.72rem;
    padding: 0.25rem 0.5rem;
    cursor: pointer;
    font-family: inherit;
    transition: color 0.15s, border-color 0.15s;
  }
  .new-project-btn:hover { color: var(--text2); border-color: var(--accent); }

  /* ── Session items ───────────────────────────────────────────────────────── */
  .session-item {
    padding: 0.45rem 0.75rem;
    cursor: pointer;
    border-left: 2px solid transparent;
    transition: background 0.1s;
    display: flex;
    align-items: flex-start;
    gap: 0.3rem;
    position: relative;
  }
  .session-item:hover    { background: var(--surface); }
  .session-item.active   { background: var(--surface-hi); border-left-color: var(--accent); }
  .session-item.compact  { padding: 0.32rem 0.75rem 0.32rem 1.35rem; }

  .sess-body { flex: 1; min-width: 0; }

  .sess-id {
    font-size: 0.78rem;
    color: var(--text2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    display: flex;
    align-items: center;
    gap: 0.3rem;
  }
  .sess-id.untitled { font-family: monospace; font-size: 0.72rem; color: var(--text4); }
  .session-item.active .sess-id { color: var(--accent-light); }

  .sess-meta {
    font-size: 0.7rem;
    color: var(--text4);
    margin-top: 0.1rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .session-item.compact .sess-meta { display: none; }

  .sess-msgs { font-size: 0.68rem; color: var(--text5); }
  .session-item.compact .sess-msgs { display: none; }

  /* Three-dot menu button */
  button.menu-btn {
    background: none;
    border: none;
    color: var(--text5);
    font-size: 0.8rem;
    line-height: 1;
    padding: 0.15rem 0.25rem;
    cursor: pointer;
    border-radius: 3px;
    flex-shrink: 0;
    visibility: hidden;
    transition: color 0.15s, background 0.15s;
  }
  .session-item:hover button.menu-btn { visibility: visible; }
  button.menu-btn:hover { color: var(--text2); background: var(--hover); }

  /* Folder children (folders layout) */
  .folder-children-guide {
    border-left: 1px solid var(--border);
    margin-left: 18px;
  }

  /* Tab bar (tabs layout) */
  .tab-bar {
    display: flex;
    border-bottom: 1px solid var(--border);
  }
  .tab-btn {
    flex: 1;
    padding: 0.55rem 0.5rem;
    font-size: 0.74rem;
    font-weight: 500;
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    color: var(--text4);
    cursor: pointer;
    transition: color 0.15s;
    font-family: inherit;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.3rem;
  }
  .tab-btn.active { color: var(--text); font-weight: 600; border-bottom-color: var(--accent); }
  .tab-count { font-size: 0.65rem; color: var(--text5); }

  /* Tab project row (taller) */
  .tab-project-row {
    padding: 0.55rem 0.75rem;
    display: flex;
    flex-direction: column;
    cursor: pointer;
    border-left: 2px solid transparent;
    transition: background 0.1s;
  }
  .tab-project-row:hover    { background: var(--hover); }
  .tab-project-row.active   { background: var(--surface); }
  .tab-project-row-header   { display: flex; align-items: center; gap: 0.45rem; }
  .tab-project-desc {
    font-size: 0.69rem;
    color: var(--text4);
    padding-left: 28px;
    margin-top: 0.15rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Expand/collapse animation (grid trick) */
  .collapse-wrapper {
    display: grid;
    grid-template-rows: 0fr;
    transition: grid-template-rows 0.22s ease;
  }
  .collapse-wrapper.open { grid-template-rows: 1fr; }
  .collapse-inner { overflow: hidden; min-height: 0; }

  /* ── Footer ──────────────────────────────────────────────────────────────── */
  .user-footer { flex-shrink: 0; position: relative; }

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
    width: 22px; height: 22px;
    border-radius: 50%;
    background: var(--surface-hi);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.65rem;
    color: var(--text2);
    flex-shrink: 0;
  }

  .username-label { font-size: 0.75rem; color: var(--text3); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .username-label.visible { max-width: 200px; opacity: 1; transition: opacity 0.18s ease 0.1s, max-width 0.2s ease; }
  .username-label.hidden  { max-width: 0;     opacity: 0; transition: opacity 0.08s ease,    max-width 0.2s ease; }

  .user-btn.centered { justify-content: center; padding: 0.6rem 0; }

  .user-menu {
    position: absolute;
    bottom: 0; left: 100%;
    margin-left: 6px;
    min-width: 200px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
    z-index: 200;
    box-shadow: 0 4px 12px rgba(0,0,0,.4);
  }
  .menu-username {
    padding: 0.55rem 0.75rem 0.3rem;
    font-size: 0.75rem;
    color: var(--text3);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    border-bottom: 1px solid var(--border);
    margin-bottom: 0.2rem;
  }
  .menu-item {
    display: block; width: 100%;
    padding: 0.55rem 0.75rem;
    font-size: 0.8rem; color: var(--text2);
    text-decoration: none;
    background: none; border: none;
    cursor: pointer; text-align: left;
    transition: background 0.1s, color 0.1s;
  }
  .menu-item:hover { background: var(--hover); color: var(--text); }

  .menu-toggle-row {
    display: flex; align-items: center; justify-content: space-between;
    padding: 0.45rem 0.75rem;
    font-size: 0.8rem; color: var(--text2);
  }
  .menu-section-label {
    padding: 0.4rem 0.75rem 0.2rem;
    font-size: 0.68rem; font-weight: 600; text-transform: uppercase; letter-spacing: 0.06em;
    color: var(--text4);
    border-top: 1px solid var(--border);
    margin-top: 0.2rem;
  }
  .theme-options { display: flex; gap: 0.3rem; padding: 0.3rem 0.75rem 0.5rem; flex-wrap: wrap; }
  .theme-btn {
    font-size: 0.72rem; padding: 0.2rem 0.5rem;
    border-radius: 4px; border: 1px solid var(--border);
    background: none; color: var(--text3);
    cursor: pointer;
    transition: background 0.15s, color 0.15s, border-color 0.15s;
  }
  .theme-btn:hover { color: var(--text); border-color: var(--accent); }
  .theme-btn.active { background: var(--surface-hi); color: var(--accent-light); border-color: var(--accent); }

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

  /* ── Confirm overlay ─────────────────────────────────────────────────────── */
  .confirm-overlay {
    position: absolute; inset: 0;
    background: color-mix(in srgb, var(--bg) 85%, transparent);
    display: flex; flex-direction: column;
    align-items: center; justify-content: center;
    gap: 0.75rem; padding: 1rem; z-index: 10;
  }
  .confirm-msg { font-size: 0.8rem; color: var(--text2); text-align: center; line-height: 1.5; }
  .confirm-btns { display: flex; gap: 0.5rem; }
  button.conf-cancel {
    padding: 0.35rem 0.8rem; border-radius: 5px; font-size: 0.8rem; cursor: pointer;
    border: 1px solid var(--border); background: none; color: var(--text2);
  }
  button.conf-cancel:hover { background: var(--surface-hi); }
  button.conf-delete {
    padding: 0.35rem 0.8rem; border-radius: 5px; font-size: 0.8rem; cursor: pointer;
    border: none; background: var(--danger-btn-hover); color: var(--danger-light);
  }
  button.conf-delete:hover { background: var(--danger-btn); }

  /* ── Context menu ────────────────────────────────────────────────────────── */
  .ctx-menu {
    position: fixed;
    min-width: 150px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 6px;
    box-shadow: 0 6px 18px rgba(0,0,0,.5);
    z-index: 1000;
    padding: 0.2rem 0;
  }
  .ctx-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: 0.45rem 0.75rem;
    font-size: 0.8rem;
    color: var(--text2);
    background: none;
    border: none;
    cursor: pointer;
    text-align: left;
    transition: background 0.1s, color 0.1s;
    font-family: inherit;
    gap: 0.35rem;
    white-space: nowrap;
  }
  .ctx-item:hover { background: var(--hover); color: var(--text); }
  .ctx-item.danger { color: #fca5a5; }
  .ctx-item.danger:hover { background: var(--danger-bg); color: #fca5a5; }
  .ctx-divider { height: 1px; background: var(--border); margin: 0.2rem 0; }

  .ctx-move-group { position: relative; }
  .ctx-submenu {
    position: absolute;
    left: 100%;
    top: 0;
    margin-left: 2px;
    min-width: 160px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 6px;
    box-shadow: 0 6px 18px rgba(0,0,0,.5);
    z-index: 1001;
    padding: 0.2rem 0;
  }
  .ctx-sub-item {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    width: 100%;
    padding: 0.4rem 0.65rem;
    font-size: 0.78rem;
    color: var(--text2);
    background: none;
    border: none;
    cursor: pointer;
    text-align: left;
    font-family: inherit;
    transition: background 0.1s, color 0.1s;
    white-space: nowrap;
  }
  .ctx-sub-item:hover { background: var(--hover); color: var(--text); }
  .ctx-sub-name { flex: 1; overflow: hidden; text-overflow: ellipsis; }
  .ctx-check { color: var(--accent); font-size: 0.75rem; }

  /* ── Edge pill handle ────────────────────────────────────────────────────── */
  .pill-handle {
    position: absolute; right: -8px; top: 50%; transform: translateY(-50%);
    width: 16px; height: 48px; z-index: 50;
    display: flex; align-items: center; justify-content: center;
    cursor: col-resize;
  }
  .pill {
    width: 4px; border-radius: 9999px; pointer-events: none;
  }
  .pill.resting  { height: 24px; background: var(--border);  transition: height 0.15s ease, background 0.15s ease; }
  .pill.active   { height: 36px; background: var(--accent);  transition: height 0.15s ease, background 0.15s ease; }
  .pill.dragging { height: 36px; background: var(--accent);  transition: none; }
</style>

<svelte:window onclick={handleWindowClick} onkeydown={handleWindowKeydown} />

<!--
  Session item snippet — reused in all three layouts.
  compact: reduces padding and hides meta/count row.
  showProjectBadge: shows a tiny ProjectIcon before the title.
-->
{#snippet sessionItem(sess, compact, showProjectBadge)}
  {@const isActive = sess.id === activeId}
  {@const parentProject = showProjectBadge ? projects.find(p => p.id === sess.project_id) : null}
  <div
    class="session-item"
    class:active={isActive}
    class:compact
    onclick={() => onSelect(sess.id)}
    oncontextmenu={e => openContextMenu(e, sess.id)}
    draggable="true"
    ondragstart={e => onDragStart(e, sess.id)}
    role="button"
    tabindex="0"
    onkeydown={e => e.key === 'Enter' && onSelect(sess.id)}
  >
    <div class="sess-body">
      <div class="sess-id" class:untitled={!sess.title}>
        {#if parentProject}
          <ProjectIcon project={parentProject} size={9} style={projectIconStyle} />
        {/if}
        {sess.title || sess.id.slice(0, 8)}
      </div>
      {#if !compact}
        <div class="sess-meta">{label(sess)}</div>
        {#if sess.message_count > 0}
          <div class="sess-msgs">{sess.message_count} msg{sess.message_count !== 1 ? 's' : ''}</div>
        {/if}
      {/if}
    </div>
    <button
      class="menu-btn"
      onclick={e => openContextMenuFromBtn(e, sess.id)}
      title="More options"
      aria-label="More options"
    >⋯</button>
  </div>
{/snippet}

<!--
  Project row snippet — drag target in sections and folders layouts.
-->
{#snippet projectRow(project, showChevron)}
  {@const isActive = project.id === activeProjectId}
  {@const isDragOver = dragOverProjectId === project.id}
  {@const projSessions = getProjectSessions(project.id)}
  <div
    class="project-row"
    class:active={isActive}
    class:drag-over={isDragOver}
    style="border-left-color:{isActive ? project.color : 'transparent'}; outline:{isDragOver ? `1px dashed ${project.color}` : 'none'}; outline-offset:-2px;"
    onclick={() => onSelectProject?.(project.id)}
    ondragover={e => onDragOver(e, project.id)}
    ondragleave={e => onDragLeave(e, project.id)}
    ondrop={e => onDrop(e, project.id)}
    role="button"
    tabindex="0"
    onkeydown={e => e.key === 'Enter' && onSelectProject?.(project.id)}
  >
    {#if showChevron}
      <button
        class="folder-chevron"
        class:open={expandedFolders.has(project.id)}
        onclick={e => { e.stopPropagation(); toggleFolder(project.id) }}
        aria-label="Toggle folder"
      >▶</button>
    {/if}
    <ProjectIcon {project} size={13} style={projectIconStyle} />
    <span class="project-name">{project.name}</span>
    <span class="project-count">{projSessions.length}</span>
  </div>
{/snippet}

<div
  class="sidebar"
  style="width: {collapsed ? '36px' : sidebarWidth + 'px'}; transition: {isResizing ? 'none' : 'width 0.2s ease'};"
>
  <div class="sidebar-content" class:visible={!collapsed} class:hidden={collapsed}>

    <!-- ── Header ─────────────────────────────────────────────────────────── -->
    <div class="sidebar-header">
      <div class="header-top-row">
        <span class="app-title">Stoa</span>
        <div class="header-actions">

          <!-- Search icon-pill -->
          <div
            class="icon-pill"
            class:expanded={searchExpanded}
            onmouseenter={() => { searchHovered = true; if (searchExpanded) searchInputEl?.focus() }}
            onmouseleave={() => searchHovered = false}
            onclick={() => searchInputEl?.focus()}
            role="search"
          >
            <svg class="pill-icon" width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
              <circle cx="5" cy="5" r="3.2"/>
              <line x1="7.3" y1="7.3" x2="10.5" y2="10.5"/>
            </svg>
            <input
              bind:this={searchInputEl}
              class="pill-input"
              class:show={searchExpanded}
              type="search"
              placeholder="Search…"
              bind:value={searchQuery}
              onfocus={() => searchFocused = true}
              onblur={() => searchFocused = false}
              onkeydown={e => {
                if (e.key === 'Escape') {
                  searchQuery = ''
                  searchFocused = false
                  searchInputEl?.blur()
                }
              }}
            />
            <button
              class="pill-clear"
              class:show={!!searchQuery}
              onclick={e => { e.stopPropagation(); searchQuery = '' }}
              aria-label="Clear search"
            >✕</button>
          </div>

          <!-- New-chat icon-pill -->
          <div
            class="icon-pill new-pill"
            class:expanded={newChatHovered}
            onmouseenter={() => newChatHovered = true}
            onmouseleave={() => newChatHovered = false}
            onclick={onNew}
            role="button"
            tabindex="0"
            onkeydown={e => e.key === 'Enter' && onNew?.()}
            aria-label="New chat"
          >
            <svg class="pill-icon" width="12" height="12" viewBox="0 0 12 12" fill="currentColor" aria-hidden="true">
              <path d="M6.5 2h-1v3.5H2v1h3.5V10h1V6.5H10v-1H6.5z"/>
            </svg>
            <span class="new-label" class:show={newChatHovered}>New chat</span>
          </div>

        </div>
      </div>
    </div>

    <!-- ── Body ───────────────────────────────────────────────────────────── -->
    <div class="session-list">

      {#if sidebarLayout === 'sections'}
        <!-- ── SECTIONS layout ─────────────────────────────────────────── -->

        <div class="section-label-row">
          <span class="section-label">Projects</span>
          <button class="section-add-btn" onclick={onNewProject} title="New project">+</button>
        </div>

        {#if projects.length === 0}
          <div class="projects-empty">
            <p>No projects yet. Group related chats, set defaults, pin files.</p>
            <button class="new-project-btn" onclick={onNewProject}>+ New project</button>
          </div>
        {:else}
          {#each projects as project (project.id)}
            {@render projectRow(project, false)}
          {/each}
        {/if}

        <div class="section-divider"></div>

        <div class="section-label-row">
          <span class="section-label">Chats</span>
          <span class="section-count">{looseSessionsList.length}</span>
        </div>

        {#each looseSessionsList as sess (sess.id)}
          {@render sessionItem(sess, false, false)}
        {/each}

      {:else if sidebarLayout === 'folders'}
        <!-- ── FOLDERS layout ───────────────────────────────────────────── -->

        {#each projects as project (project.id)}
          {@render projectRow(project, true)}
          {#if expandedFolders.has(project.id)}
            {@const children = getProjectSessions(project.id)}
            <div class="folder-children-guide">
              {#if children.length === 0}
                <div style="padding:0.3rem 0.75rem; font-size:0.72rem; color:var(--text5); font-style:italic;">empty</div>
              {:else}
                {#each children as sess (sess.id)}
                  {@render sessionItem(sess, true, false)}
                {/each}
              {/if}
            </div>
          {/if}
        {/each}

        {#if projects.length === 0}
          <div class="projects-empty">
            <p>No projects yet. Group related chats, set defaults, pin files.</p>
            <button class="new-project-btn" onclick={onNewProject}>+ New project</button>
          </div>
        {/if}

        <div class="section-divider"></div>

        <div class="section-label-row">
          <span class="section-label">Loose chats</span>
          <span class="section-count">{looseSessionsList.length}</span>
        </div>

        {#each looseSessionsList as sess (sess.id)}
          {@render sessionItem(sess, false, false)}
        {/each}

      {:else}
        <!-- ── TABS layout ──────────────────────────────────────────────── -->

        <div class="tab-bar">
          <button
            class="tab-btn"
            class:active={sidebarTab === 'chats'}
            onclick={() => sidebarTab = 'chats'}
          >
            Chats
            <span class="tab-count">{sessions.length}</span>
          </button>
          <button
            class="tab-btn"
            class:active={sidebarTab === 'projects'}
            onclick={() => sidebarTab = 'projects'}
          >
            Projects
            <span class="tab-count">{projects.length}</span>
          </button>
        </div>

        {#if sidebarTab === 'chats'}
          {#each sessions as sess (sess.id)}
            {@const hasBadge = !!sess.project_id && !!projects.find(p => p.id === sess.project_id)}
            {@render sessionItem(sess, false, hasBadge)}
          {/each}
        {:else}
          {#if projects.length === 0}
            <div class="projects-empty">
              <p>No projects yet.</p>
              <button class="new-project-btn" onclick={onNewProject}>+ New project</button>
            </div>
          {:else}
            {#each projects as project (project.id)}
              {@const isActive = project.id === activeProjectId}
              {@const isExpanded = expandedTabProjects.has(project.id)}
              {@const isDragOver = dragOverProjectId === project.id}
              {@const projSessions = getProjectSessions(project.id)}
              <div
                class="tab-project-row"
                class:active={isActive}
                class:drag-over={isDragOver}
                style="border-left-color:{isActive ? project.color : 'transparent'}; outline:{isDragOver ? `1px dashed ${project.color}` : 'none'}; outline-offset:-2px;"
                onclick={() => selectTabProject(project.id)}
                ondragover={e => onDragOver(e, project.id)}
                ondragleave={e => onDragLeave(e, project.id)}
                ondrop={e => onDrop(e, project.id)}
                role="button"
                tabindex="0"
                onkeydown={e => e.key === 'Enter' && selectTabProject(project.id)}
              >
                <div class="tab-project-row-header">
                  <ProjectIcon {project} size={13} style={projectIconStyle} />
                  <span class="project-name">{project.name}</span>
                  <span
                    class="folder-chevron"
                    class:open={isExpanded}
                    style="display:inline-flex;"
                  >▶</span>
                </div>
                <div class="tab-project-desc">{project.description}</div>
              </div>
              <div class="collapse-wrapper" class:open={isExpanded}>
                <div class="collapse-inner">
                  {#if projSessions.length === 0}
                    <div style="padding:0.3rem 0.75rem; font-size:0.72rem; color:var(--text5); font-style:italic;">empty</div>
                  {:else}
                    {#each projSessions as sess (sess.id)}
                      {@render sessionItem(sess, true, false)}
                    {/each}
                  {/if}
                </div>
              </div>
            {/each}
          {/if}
        {/if}
      {/if}

    </div>
  </div>

  <!-- ── User footer ──────────────────────────────────────────────────────── -->
  {#if user}
    <div class="user-footer">
      {#if userMenuOpen}
        <div class="user-menu" onclick={e => e.stopPropagation()}>
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
            {#each themes as t (t.id)}
              <button
                class="theme-btn"
                class:active={theme === t.id}
                onclick={() => onThemeChange?.(t.id)}
              >{t.label}</button>
            {/each}
          </div>
          <button class="menu-item" onclick={() => {
            apiFetch('/auth/logout', { method: 'POST' }).then(() => { window.location.href = '/' })
          }}>Sign out</button>
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

  <!-- ── Context menu ─────────────────────────────────────────────────────── -->
  {#if contextMenu}
    {@const ctxSess = getSession(contextMenu.sessionId)}
    <div
      class="ctx-menu"
      style="left:{contextMenu.x}px; top:{contextMenu.y}px;"
      onclick={e => e.stopPropagation()}
    >
      {#if projects.length > 0}
        <div
          class="ctx-move-group"
          role="none"
          onmouseenter={() => contextSubmenuOpen = true}
          onmouseleave={() => contextSubmenuOpen = false}
        >
          <div class="ctx-item" style="cursor:default;">
            <span>Move to project</span>
            <span style="font-size:0.7rem; color:var(--text4);">›</span>
          </div>
          {#if contextSubmenuOpen}
            <div class="ctx-submenu">
              {#each projects as proj (proj.id)}
                <button
                  class="ctx-sub-item"
                  onclick={() => { onMoveSession?.(contextMenu.sessionId, proj.id); closeContextMenu() }}
                >
                  <ProjectIcon project={proj} size={10} style={projectIconStyle} />
                  <span class="ctx-sub-name">{proj.name}</span>
                  {#if ctxSess?.project_id === proj.id}
                    <span class="ctx-check">✓</span>
                  {/if}
                </button>
              {/each}
              {#if ctxSess?.project_id}
                <div class="ctx-divider"></div>
                <button
                  class="ctx-sub-item"
                  onclick={() => { onMoveSession?.(contextMenu.sessionId, null); closeContextMenu() }}
                >Remove from project</button>
              {/if}
            </div>
          {/if}
        </div>
        <div class="ctx-divider"></div>
      {/if}
      <button
        class="ctx-item danger"
        onclick={() => requestDelete(null, contextMenu.sessionId)}
      >Delete</button>
    </div>
  {/if}

  <!-- ── Delete confirm overlay ───────────────────────────────────────────── -->
  {#if confirmId}
    <div class="confirm-overlay">
      <p class="confirm-msg">Delete this conversation?<br>This cannot be undone.</p>
      <div class="confirm-btns">
        <button class="conf-cancel" onclick={cancelDelete}>Cancel</button>
        <button class="conf-delete" onclick={confirmDelete}>Delete</button>
      </div>
    </div>
  {/if}

  <!-- ── Edge pill handle ─────────────────────────────────────────────────── -->
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
