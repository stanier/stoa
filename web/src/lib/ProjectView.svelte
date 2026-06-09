<script>
  import ProjectIcon from './ProjectIcon.svelte'
  import { formatDate } from './utils.js'

  let {
    project,
    sessions = [],
    projectIconStyle = 'color-dot',
    onBack,
    onNewChat,
    onUpdate,
    onDelete,
    onSelectSession,
  } = $props()

  let tab = $state('threads')
  let editing = $state(false)

  let editName = $state('')
  let editDescription = $state('')
  let editInstructions = $state('')

  let projectSessions = $derived(sessions.filter(s => s.project_id === project.id))

  function startEdit() {
    editName = project.name
    editDescription = project.description
    editInstructions = project.instructions
    editing = true
  }

  function cancelEdit() {
    editing = false
  }

  function saveEdit() {
    onUpdate?.({
      ...project,
      name: editName,
      description: editDescription,
      instructions: editInstructions,
      updated_at: new Date().toISOString(),
    })
    editing = false
  }

  function relTime(dateStr) {
    if (!dateStr) return '—'
    const diff = Math.floor((Date.now() - new Date(dateStr)) / 1000)
    if (diff < 60)    return 'just now'
    if (diff < 3600)  return `${Math.floor(diff / 60)}m ago`
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
    return `${Math.floor(diff / 86400)}d ago`
  }

  function fileExt(filename) {
    const parts = filename.split('.')
    if (parts.length < 2) return 'FIL'
    return parts[parts.length - 1].slice(0, 3).toUpperCase()
  }

  const ALL_TOOLS_COUNT = 11
</script>

<div class="project-view">
  <!-- Header band -->
  <div class="header-band">
    <button class="back-link" onclick={() => onBack?.()}>← All chats</button>

    <div class="title-row">
      <!-- Icon tile -->
      <div class="icon-tile" style="background:{project.color}22; border-color:{project.color}55;">
        {#if projectIconStyle === 'emoji'}
          <span style="font-size:1.6rem;">{project.emoji}</span>
        {:else}
          <span style="font-size:1.6rem; color:{project.color}; font-family:monospace;">{project.glyph}</span>
        {/if}
      </div>

      <!-- Title block -->
      <div class="title-block">
        {#if editing}
          <input class="edit-input name-input" bind:value={editName} placeholder="Project name" />
          <input class="edit-input desc-input" bind:value={editDescription} placeholder="Short description" />
        {:else}
          <h1>{project.name}</h1>
          <p>{project.description}</p>
        {/if}
        <div class="meta-row">
          <span>{projectSessions.length} thread{projectSessions.length !== 1 ? 's' : ''}</span>
          <span>{project.files.length} file{project.files.length !== 1 ? 's' : ''}</span>
          <span>Updated {relTime(project.updated_at)}</span>
        </div>
      </div>

      <!-- Action buttons -->
      <div class="action-btns">
        {#if editing}
          <button class="btn ghost" onclick={cancelEdit}>Cancel</button>
          <button class="btn accent" onclick={saveEdit}>Save</button>
        {:else}
          <button class="btn ghost" onclick={startEdit}>Edit</button>
          <button class="btn accent" onclick={() => onNewChat?.(project.id)}>
            <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor" aria-hidden="true">
              <path d="M6.5 2h-1v3.5H2v1h3.5V10h1V6.5H10v-1H6.5z"/>
            </svg>
            New chat
          </button>
        {/if}
      </div>
    </div>

    <!-- Tabs bar -->
    <div class="tabs-bar">
      {#each [
        { id: 'threads',      label: `Threads (${projectSessions.length})` },
        { id: 'instructions', label: 'Instructions' },
        { id: 'files',        label: `Files (${project.files.length})` },
        { id: 'settings',     label: 'Defaults' },
      ] as t (t.id)}
        <button
          class="tab-btn"
          class:active={tab === t.id}
          style={tab === t.id ? `border-bottom-color:${project.color}` : ''}
          onclick={() => tab = t.id}
        >{t.label}</button>
      {/each}
    </div>
  </div>

  <!-- Tab body -->
  <div class="tab-body" style="grid-template-columns:{tab === 'threads' ? '1fr 280px' : '1fr'};">

    {#if tab === 'threads'}
      <!-- Thread list -->
      <div class="thread-list">
        {#if projectSessions.length === 0}
          <div class="empty-state">
            <p>No threads yet.</p>
            <button class="btn accent" onclick={() => onNewChat?.(project.id)}>Start a chat</button>
          </div>
        {:else}
          {#each projectSessions as sess (sess.id)}
            <div
              class="thread-card"
              onclick={() => onSelectSession?.(sess.id)}
              role="button"
              tabindex="0"
              onkeydown={e => e.key === 'Enter' && onSelectSession?.(sess.id)}
            >
              <div class="thread-info">
                <div class="thread-title" class:untitled={!sess.title}>
                  {sess.title || sess.id.slice(0, 8)}
                </div>
                <div class="thread-meta">
                  {relTime(sess.last_active_at)} · {sess.message_count} msg{sess.message_count !== 1 ? 's' : ''}
                </div>
              </div>
              <span class="thread-chevron">›</span>
            </div>
          {/each}
        {/if}
      </div>

      <!-- About card -->
      <div class="about-col">
        <div class="card">
          <div class="card-label">About</div>
          <p class="about-desc">{project.description}</p>
          <div class="about-rows">
            <div class="about-row">
              <span class="about-key">Model</span>
              <span class="about-val mono">{project.default_model.split('/').pop()}</span>
            </div>
            <div class="about-row">
              <span class="about-key">Tools</span>
              <span class="about-val">{project.default_tools.length} enabled</span>
            </div>
            <div class="about-row">
              <span class="about-key">Files</span>
              <span class="about-val">{project.files.length} attached</span>
            </div>
            <div class="about-row">
              <span class="about-key">Created</span>
              <span class="about-val">{formatDate(project.created_at)}</span>
            </div>
          </div>
        </div>
      </div>

    {:else if tab === 'instructions'}
      <div>
        <div class="card">
          <div class="card-label-row">
            <span class="card-label">Custom instructions</span>
            <span class="card-hint">Applied to every chat in this project</span>
          </div>
          {#if editing}
            <textarea
              class="edit-textarea"
              bind:value={editInstructions}
              placeholder="Add custom instructions for this project…"
            ></textarea>
          {:else if project.instructions}
            <p class="instructions-text">{project.instructions}</p>
          {:else}
            <p class="instructions-empty">No instructions set. Click Edit to add some.</p>
          {/if}
        </div>
      </div>

    {:else if tab === 'files'}
      <div>
        <div class="card">
          <div class="card-label-row">
            <span class="card-label">Attached files</span>
            <button class="upload-btn">+ Upload</button>
          </div>
          {#if project.files.length === 0}
            <div class="files-empty">No files attached. Drag files here or click upload.</div>
          {:else}
            <div class="file-list">
              {#each project.files as file (file.name)}
                <div class="file-row">
                  <div class="file-chip">{fileExt(file.name)}</div>
                  <span class="file-name">{file.name}</span>
                  <span class="file-size">{file.size}</span>
                  <button class="file-remove" title="Remove file">✕</button>
                </div>
              {/each}
            </div>
            <p class="files-hint">Files attached to a project are added to the context of every chat started here.</p>
          {/if}
        </div>
      </div>

    {:else if tab === 'settings'}
      <div class="defaults-col">
        <div class="card">
          <div class="card-label">Default model</div>
          <p class="mono setting-val">{project.default_model}</p>
          <p class="setting-hint">New chats in this project start with this model. Can be overridden per chat.</p>
        </div>
        <div class="card">
          <div class="card-label-row">
            <span class="card-label">Enabled tools</span>
            <span class="card-hint">{project.default_tools.length} of {ALL_TOOLS_COUNT}</span>
          </div>
          {#if project.default_tools.length === 0}
            <p class="tools-empty">None — chats use account defaults.</p>
          {:else}
            <div class="tool-chips">
              {#each project.default_tools as tool (tool)}
                <span class="tool-chip">{tool}</span>
              {/each}
            </div>
          {/if}
        </div>
        <div class="card danger-card">
          <div class="card-label danger-label">Danger zone</div>
          <button class="danger-btn" onclick={() => onDelete?.(project.id)}>Delete project</button>
          <p class="setting-hint">Threads inside this project become loose chats. Files and instructions are deleted.</p>
        </div>
      </div>
    {/if}
  </div>
</div>

<style>
  .project-view {
    flex: 1;
    overflow-y: auto;
    background: var(--bg);
    color: var(--text);
    font-family: inherit;
  }

  .header-band {
    padding: 1.5rem 2rem 0;
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .back-link {
    background: none;
    border: none;
    color: var(--text3);
    font-size: 0.8rem;
    padding: 0.25rem 0;
    cursor: pointer;
    align-self: flex-start;
    transition: color 0.15s;
    font-family: inherit;
  }
  .back-link:hover { color: var(--text2); }

  .title-row {
    display: flex;
    gap: 1rem;
    align-items: flex-start;
  }

  .icon-tile {
    width: 56px;
    height: 56px;
    border-radius: 12px;
    border: 1px solid;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
  }

  .title-block { flex: 1; min-width: 0; }
  .title-block h1 { font-size: 1.5rem; font-weight: 600; line-height: 1.2; }
  .title-block p  { font-size: 0.88rem; color: var(--text3); margin: 0.3rem 0 0; line-height: 1.5; }

  .meta-row {
    display: flex;
    gap: 1.25rem;
    margin-top: 0.4rem;
    font-size: 0.72rem;
    color: var(--text4);
  }

  .action-btns {
    display: flex;
    gap: 0.5rem;
    flex-shrink: 0;
    align-items: flex-start;
    padding-top: 0.2rem;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    padding: 0.45rem 0.85rem;
    font-size: 0.82rem;
    border-radius: 6px;
    cursor: pointer;
    font-family: inherit;
    transition: background 0.15s, color 0.15s, border-color 0.15s;
    white-space: nowrap;
  }
  .btn.ghost {
    background: none;
    border: 1px solid var(--border);
    color: var(--text2);
  }
  .btn.ghost:hover { background: var(--hover); color: var(--text); }
  .btn.accent {
    background: var(--accent);
    border: 1px solid transparent;
    color: var(--bg);
    font-weight: 500;
  }
  .btn.accent:hover { background: var(--accent-light); }

  .tabs-bar {
    display: flex;
    gap: 1.25rem;
    border-bottom: 1px solid var(--border);
    margin-top: 0.5rem;
  }
  .tab-btn {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    padding: 0.55rem 0.1rem;
    margin-bottom: -1px;
    font-size: 0.82rem;
    color: var(--text4);
    font-weight: 400;
    cursor: pointer;
    transition: color 0.15s;
    font-family: inherit;
  }
  .tab-btn.active { color: var(--text); font-weight: 500; }

  .tab-body {
    display: grid;
    gap: 1.5rem;
    padding: 1.5rem 2rem 3rem;
    max-width: 1080px;
  }

  /* Cards */
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1.25rem;
    margin-bottom: 1rem;
  }
  .card-label {
    font-size: 0.68rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--text4);
    margin-bottom: 0.65rem;
    display: block;
  }
  .card-label-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 0.65rem;
  }
  .card-label-row .card-label { margin-bottom: 0; }
  .card-hint { font-size: 0.65rem; color: var(--text4); }

  /* Threads */
  .thread-list { display: flex; flex-direction: column; gap: 0.4rem; }
  .thread-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.7rem 0.9rem;
    display: flex;
    gap: 0.75rem;
    cursor: pointer;
    transition: background 0.1s;
  }
  .thread-card:hover { background: var(--surface-hi); }
  .thread-info { flex: 1; min-width: 0; }
  .thread-title {
    font-size: 0.9rem;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .thread-title.untitled { font-family: monospace; color: var(--text4); }
  .thread-meta { font-size: 0.72rem; color: var(--text4); margin-top: 0.15rem; }
  .thread-chevron { color: var(--text5); font-size: 0.9rem; align-self: center; }

  .empty-state {
    border: 1px dashed var(--border);
    border-radius: 10px;
    padding: 3rem 1rem;
    text-align: center;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 1rem;
    color: var(--text4);
    font-size: 0.88rem;
  }

  /* About card */
  .about-col { align-self: start; }
  .about-desc { font-size: 0.78rem; color: var(--text3); line-height: 1.55; margin-bottom: 0.75rem; }
  .about-rows { display: flex; flex-direction: column; gap: 0.5rem; }
  .about-row { display: flex; justify-content: space-between; font-size: 0.74rem; color: var(--text3); }
  .about-key { color: var(--text4); }
  .mono { font-family: monospace; }

  /* Instructions */
  .instructions-text { font-size: 0.88rem; color: var(--text2); line-height: 1.65; white-space: pre-wrap; }
  .instructions-empty { font-size: 0.88rem; color: var(--text4); font-style: italic; }

  /* Files */
  .files-empty {
    border: 1px dashed var(--border);
    border-radius: 8px;
    padding: 2rem 1rem;
    text-align: center;
    color: var(--text4);
    font-size: 0.84rem;
  }
  .file-list { display: flex; flex-direction: column; }
  .file-row {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    padding: 0.55rem 0.2rem;
    border-bottom: 1px solid var(--border);
    font-size: 0.84rem;
  }
  .file-row:last-child { border-bottom: none; }
  .file-chip {
    width: 22px;
    height: 22px;
    border-radius: 4px;
    background: var(--depth);
    display: flex;
    align-items: center;
    justify-content: center;
    font-family: monospace;
    font-size: 0.7rem;
    color: var(--text3);
    flex-shrink: 0;
  }
  .file-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text2);
  }
  .file-size { font-size: 0.72rem; color: var(--text4); flex-shrink: 0; }
  .file-remove {
    background: none;
    border: none;
    color: var(--text4);
    font-size: 0.85rem;
    cursor: pointer;
    padding: 0.1rem 0.2rem;
    border-radius: 3px;
    flex-shrink: 0;
    transition: color 0.15s;
  }
  .file-remove:hover { color: var(--danger-light); }
  .files-hint { font-size: 0.72rem; color: var(--text5); line-height: 1.45; margin-top: 0.75rem; }
  .upload-btn {
    background: none;
    border: 1px solid var(--border);
    color: var(--text3);
    font-size: 0.72rem;
    padding: 0.25rem 0.55rem;
    border-radius: 5px;
    cursor: pointer;
    font-family: inherit;
    transition: background 0.15s, color 0.15s;
  }
  .upload-btn:hover { background: var(--hover); color: var(--text); }

  /* Defaults */
  .defaults-col { display: flex; flex-direction: column; gap: 1rem; }
  .defaults-col .card { margin-bottom: 0; }
  .setting-val { font-size: 0.88rem; color: var(--text); margin-bottom: 0.4rem; }
  .setting-hint { font-size: 0.72rem; color: var(--text4); line-height: 1.45; margin-top: 0.4rem; }
  .tool-chips { display: flex; flex-wrap: wrap; gap: 0.35rem; }
  .tool-chip {
    font-size: 0.72rem;
    font-family: monospace;
    color: var(--accent-light);
    background: var(--depth);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.2rem 0.5rem;
  }
  .tools-empty { font-size: 0.84rem; color: var(--text4); font-style: italic; }

  .danger-card { border-color: var(--danger-border); }
  .danger-label { color: var(--danger-light) !important; }
  .danger-btn {
    border: 1px solid var(--danger-border);
    color: var(--danger-light);
    background: none;
    border-radius: 6px;
    padding: 0.4rem 0.85rem;
    font-size: 0.82rem;
    cursor: pointer;
    font-family: inherit;
    transition: background 0.15s;
  }
  .danger-btn:hover { background: var(--danger-bg); }

  /* Edit mode */
  .edit-input {
    display: block;
    width: 100%;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text);
    font-size: 0.85rem;
    padding: 0.45rem 0.6rem;
    outline: none;
    font-family: inherit;
    margin-bottom: 0.4rem;
  }
  .edit-input:focus { border-color: var(--accent); }
  .name-input { font-size: 1.35rem; font-weight: 600; }
  .edit-textarea {
    display: block;
    width: 100%;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text);
    font-size: 0.88rem;
    padding: 0.45rem 0.6rem;
    outline: none;
    font-family: inherit;
    min-height: 200px;
    resize: vertical;
    line-height: 1.6;
  }
  .edit-textarea:focus { border-color: var(--accent); }
</style>
