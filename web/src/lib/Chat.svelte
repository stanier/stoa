<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import SettingsPanel from './SettingsPanel.svelte'
  import ToolsPanel from './ToolsPanel.svelte'
  import { marked } from 'marked'

  function renderMarkdown(text) {
    return marked.parse(text ?? '', { breaks: true })
  }

  // sessionId and onSessionEnd are passed in from App — Chat no longer owns
  // session creation. {#key activeId} in App remounts Chat on session switch,
  // so state is naturally reset.
  let { user, sessionId, sessionTitle = '', onSessionEnd } = $props()

  // ── Message model ─────────────────────────────────────────────────────────────
  // Each item in `messages` is one of:
  //   { kind: 'user',       content }
  //   { kind: 'assistant',  content, thinking, streaming }
  //   { kind: 'tool',       toolName, toolUseId, params, result, isError, pending }
  //   { kind: 'compaction', summary, before, after }

  let settingsOpen  = $state(false)
  let toolsOpen     = $state(false)
  let messages      = $state([])
  let input         = $state('')
  let connected     = $state(false)
  let error         = $state(null)
  let permRequest   = $state(null)
  let pendingFiles  = $state([])   // [{ name, path, size }] — uploaded, not yet sent
  let uploading     = $state(false)
  let fileInput     = null
  let ws            = null
  let msgEnd        = null

  // ── Lifecycle ─────────────────────────────────────────────────────────────────

  onMount(async () => {
    await loadHistory()
    openWS()
  })

  const ATTACH_PREFIX = '[Attachments — use mcp_filesystem_read_file to read them]\n'

  function parseUserContent(content) {
    // Split off the attachment header block from user message text.
    // Returns { text, attachments: [{name, path, size}] }
    if (!content.startsWith(ATTACH_PREFIX)) return { text: content, attachments: [] }
    const rest = content.slice(ATTACH_PREFIX.length)
    const lines = rest.split('\n')
    const attachments = []
    let textStart = 0
    for (let i = 0; i < lines.length; i++) {
      const m = lines[i].match(/^- (.+?) \(path: (.+?), size: (.+?)\)$/)
      if (m) {
        attachments.push({ name: m[1], path: m[2], size: m[3] })
        textStart = i + 1
      } else {
        break
      }
    }
    // Skip blank separator line after attachment list
    if (textStart < lines.length && lines[textStart] === '') textStart++
    const text = lines.slice(textStart).join('\n')
    return { text, attachments }
  }

  async function loadHistory() {
    try {
      const res = await fetch(`/api/sessions/${sessionId}/messages`)
      if (!res.ok) return
      const raw = await res.json()
      if (!Array.isArray(raw)) return
      messages = raw.flatMap(m => {
        switch (m.Role) {
          case 'user': {
            // Tool errors/results are stored as Role='user' with formatted content.
            const errMatch  = m.Content.match(/^\[Tool error for ([^\]]+)\]: ([\s\S]*)$/)
            const resMatch  = m.Content.match(/^\[Tool result for ([^\]]+)\]: ([\s\S]*)$/)
            if (errMatch) {
              return [{ kind: 'tool', toolName: errMatch[1], toolUseId: crypto.randomUUID(),
                        params: null, result: errMatch[2], isError: true, pending: false }]
            }
            if (resMatch) {
              return [{ kind: 'tool', toolName: resMatch[1], toolUseId: crypto.randomUUID(),
                        params: null, result: resMatch[2], isError: false, pending: false }]
            }
            const { text, attachments } = parseUserContent(m.Content)
            return [{ kind: 'user', content: text, attachments }]
          }
          case 'assistant': {
            return [{ kind: 'assistant', content: m.Content, thinking: m.Thinking ?? '', streaming: false }]
          }
          case 'tool':
            return [{
              kind:      'tool',
              toolName:  m.ToolName,
              toolUseId: m.ToolUseID,
              params:    null,
              result:    m.Content,
              isError:   !m.ToolSuccess,
              pending:   false,
            }]
          case 'summary':
            return [{ kind: 'compaction', summary: m.Content, before: 0, after: 0 }]
          default:
            return []
        }
      })
      await scrollToBottom()
    } catch (_) {}
  }

  onDestroy(() => ws?.close())

  // ── WebSocket ─────────────────────────────────────────────────────────────────

  function openWS() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    ws = new WebSocket(`${proto}://${location.host}/ws/${sessionId}`)
    ws.onopen  = () => { connected = true; error = null }
    ws.onclose = () => { connected = false }
    ws.onerror = () => { error = 'Connection lost — reload to reconnect.' }
    ws.onmessage = ({ data }) => {
      try { handleEvent(JSON.parse(data)) } catch {}
    }
  }

  // ── Event handling ────────────────────────────────────────────────────────────

  function handleEvent(ev) {
    switch (ev.type) {

      case 'chunk': {
        const content  = ev.chunk?.content  ?? ''
        const thinking = ev.chunk?.thinking ?? ''
        const last = messages.at(-1)
        if (last?.kind === 'assistant' && last.streaming) {
          if (content)  last.content  += content
          if (thinking) last.thinking += thinking
          messages = messages
        } else {
          messages = [...messages, {
            kind: 'assistant', content, thinking, streaming: true
          }]
        }
        scrollToBottom()
        break
      }

      case 'done': {
        const last = messages.findLast(m => m.kind === 'assistant')
        if (last) {
          if (ev.done?.full_content  !== undefined) last.content  = ev.done.full_content
          if (ev.done?.full_thinking !== undefined) last.thinking = ev.done.full_thinking
          last.streaming = false
          messages = messages
        }
        scrollToBottom()
        break
      }

      case 'tool_invoked': {
        // Only show as a visible row when it doesn't require permission
        // (permission-requiring tools get their own modal + row via perm_request).
        if (!ev.tool_invoked?.requires_permission) {
          messages = [...messages, {
            kind:      'tool',
            toolName:  ev.tool_invoked.tool_name,
            toolUseId: ev.tool_invoked.tool_use_id,
            params:    ev.tool_invoked.params,
            result:    null,
            isError:   false,
            pending:   true,
          }]
        }
        break
      }

      case 'tool_result': {
        // Find the matching pending tool row and resolve it.
        const row = messages.findLast(
          m => m.kind === 'tool' && m.toolUseId === ev.tool_result?.tool_use_id
        )
        if (row) {
          row.result  = ev.tool_result.result
          row.isError = ev.tool_result.is_error ?? false
          row.pending = false
          messages = messages
        }
        break
      }

      case 'perm_request': {
        const pr = ev.perm_request
        // Add a placeholder tool row that will resolve when the user decides.
        messages = [...messages, {
          kind:      'tool',
          toolName:  pr.tool_name,
          toolUseId: pr.request_id,
          params:    pr.params,
          result:    null,
          isError:   false,
          pending:   true,
        }]
        permRequest = {
          requestId:   pr.request_id,
          toolName:    pr.tool_name,
          description: pr.description,
          params:      pr.params,
        }
        scrollToBottom()
        break
      }

      case 'perm_mention': {
        // Tool ran silently — show a brief inline notice.
        messages = [...messages, {
          kind:      'tool',
          toolName:  ev.perm_mention?.tool_name,
          toolUseId: crypto.randomUUID(),
          params:    null,
          result:    ev.perm_mention?.description ?? 'ran silently',
          isError:   false,
          pending:   false,
        }]
        break
      }

      case 'compaction': {
        messages = [...messages, {
          kind:    'compaction',
          summary: ev.compaction?.summary ?? '',
          before:  ev.compaction?.messages_before ?? 0,
          after:   ev.compaction?.messages_after  ?? 0,
        }]
        scrollToBottom()
        break
      }

      case 'error': {
        error = ev.error?.message ?? 'Unknown error'
        if (ev.error?.fatal) { connected = false; onSessionEnd?.() }
        break
      }
    }
  }

  // ── Attachments ───────────────────────────────────────────────────────────────

  async function onFileSelected(e) {
    const files = Array.from(e.target.files ?? [])
    if (!files.length) return
    fileInput.value = ''
    uploading = true
    for (const file of files) {
      const form = new FormData()
      form.append('file', file)
      try {
        const res = await fetch('/api/upload', { method: 'POST', body: form })
        if (!res.ok) { error = `Upload failed: ${file.name}`; continue }
        const data = await res.json()
        pendingFiles = [...pendingFiles, { name: data.name, path: data.path, size: data.size }]
      } catch (e) {
        error = `Upload error: ${file.name}`
      }
    }
    uploading = false
  }

  function removeFile(path) {
    pendingFiles = pendingFiles.filter(f => f.path !== path)
  }

  function fmtSize(bytes) {
    if (bytes < 1024) return `${bytes} B`
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  }

  // ── Sending ───────────────────────────────────────────────────────────────────

  function send() {
    const text = input.trim()
    if ((!text && !pendingFiles.length) || !connected || permRequest) return
    input = ''

    // Prepend file references in the same format the Zulip bridge uses,
    // so the LLM knows to use mcp_filesystem tools to read them.
    let content = text
    if (pendingFiles.length) {
      const refs = pendingFiles.map(f =>
        `- ${f.name} (path: ${f.path}, size: ${fmtSize(f.size)})`
      ).join('\n')
      const prefix = `[Attachments — use mcp_filesystem_read_file to read them]\n${refs}`
      content = text ? `${prefix}\n\n${text}` : prefix
      pendingFiles = []
    }

    const sentAttachments = pendingFiles.length ? [...pendingFiles] : []
    messages = [...messages, { kind: 'user', content: text || '', attachments: sentAttachments.map(f => ({ name: f.name, path: f.path, size: fmtSize(f.size) })) }]
    scrollToBottom()
    ws.send(JSON.stringify({ type: 'send_message', send_message: { content } }))
    onSessionEnd?.()
  }

  function onKeydown(e) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
  }

  function respondPerm(approved) {
    if (!permRequest) return
    // Resolve the placeholder tool row.
    const row = messages.findLast(m => m.kind === 'tool' && m.toolUseId === permRequest.requestId)
    if (row) {
      row.result  = approved ? 'Approved' : 'Denied'
      row.isError = !approved
      row.pending = false
      messages = messages
    }
    ws.send(JSON.stringify({
      type: 'perm_response',
      perm_response: { request_id: permRequest.requestId, approved },
    }))
    permRequest = null
  }

  async function scrollToBottom() {
    await tick()
    msgEnd?.scrollIntoView({ behavior: 'smooth' })
  }

  // ── Helpers ───────────────────────────────────────────────────────────────────

  function fmtParams(params) {
    if (!params) return ''
    try {
      const obj = typeof params === 'string' ? JSON.parse(params) : params
      return JSON.stringify(obj, null, 2)
    } catch {
      return String(params)
    }
  }
</script>

<style>
  /* ── Layout ── */
  .outer  { display: flex; flex: 1; min-width: 0; overflow: hidden; }
  .layout { display: flex; flex-direction: column; flex: 1; min-width: 0; overflow: hidden; }

  /* ── Header ── */
  header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 0.75rem 1.25rem;
    background: #1a1d27; border-bottom: 1px solid #2d3148; flex-shrink: 0;
  }
  .session-title { font-weight: 600; font-size: 1rem; color: #e2e8f0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .header-right { display: flex; align-items: center; gap: 0.75rem; font-size: 0.85rem; color: #64748b; margin-left: auto; }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: #ef4444; transition: background .3s; }
  .dot.ok { background: #22c55e; }
  a.logout { color: #64748b; font-size: 0.8rem; text-decoration: none; }
  a.logout:hover { color: #e2e8f0; }
  button.gear {
    background: none; border: none; color: #475569; cursor: pointer;
    font-size: 0.95rem; padding: 0 0.2rem; line-height: 1;
    transition: color 0.15s;
  }
  button.gear:hover, button.gear.active { color: #a5b4fc; }

  /* ── Messages ── */
  .messages { flex: 1; overflow-y: auto; padding: 1.25rem 12rem; display: flex; flex-direction: column; gap: 0.75rem; }

  /* ── User bubble ── */
  .msg-user { display: flex; justify-content: flex-end; }
  .bubble-user {
    background: #6366f1; color: #fff;
    padding: 0.6rem 0.9rem; border-radius: 10px 10px 2px 10px;
    max-width: 70%; white-space: pre-wrap; word-break: break-word;
    font-size: 0.925rem; line-height: 1.6;
  }

  /* ── History attachment pills (inside user bubble) ── */
  .hist-attachments {
    display: flex; flex-wrap: wrap; gap: 0.3rem; margin-bottom: 0.4rem;
  }
  .hist-pill {
    background: rgba(255,255,255,0.12); border-radius: 4px;
    padding: 0.15rem 0.45rem; font-size: 0.75rem;
  }
  .hist-size { opacity: 0.7; }

  /* ── Assistant bubble ── */
  .msg-assistant { display: flex; flex-direction: column; gap: 0.4rem; max-width: 85%; }

  .thinking-box {
    background: #12151e; border: 1px solid #2d3148; border-radius: 6px;
    padding: 0.5rem 0.75rem; font-size: 0.8rem; color: #64748b;
    font-family: monospace; white-space: pre-wrap; word-break: break-word;
  }
  .thinking-box summary {
    cursor: pointer; color: #94a3b8; font-family: inherit;
    list-style: none; display: flex; align-items: center; gap: 0.4rem;
    flex-wrap: wrap;
  }
  .thinking-box summary::before { flex-shrink: 0; }
  .thinking-box summary::before { content: '▶'; font-size: 0.6rem; transition: transform .2s; }
  details[open] summary::before { transform: rotate(90deg); }
  .thinking-content { margin-top: 0.4rem; }
  .thinking-live {
    display: block; width: 100%; margin-top: 0.25rem;
    color: #475569; font-size: 0.75rem; font-family: monospace;
    white-space: pre-wrap; word-break: break-word;
    opacity: 0.85;
    max-height: 3.6em; overflow: hidden;
  }

  .bubble-assistant {
    background: #1e2235; border: 1px solid #2d3148;
    padding: 0.6rem 0.9rem; border-radius: 10px 10px 10px 2px;
    word-break: break-word;
    font-size: 0.925rem; line-height: 1.6;
  }
  /* Markdown rendering */
  .md-content :global(p)  { margin: 0 0 0.6em; }
  .md-content :global(p:last-child) { margin-bottom: 0; }
  .md-content :global(h1), .md-content :global(h2), .md-content :global(h3),
  .md-content :global(h4), .md-content :global(h5), .md-content :global(h6) {
    margin: 0.8em 0 0.3em; font-weight: 600; line-height: 1.3;
  }
  .md-content :global(ul), .md-content :global(ol) { margin: 0.3em 0 0.6em 1.4em; padding: 0; }
  .md-content :global(li) { margin: 0.15em 0; }
  .md-content :global(code) {
    background: #0f1117; border: 1px solid #2d3148;
    padding: 0.1em 0.35em; border-radius: 4px; font-size: 0.875em; font-family: monospace;
  }
  .md-content :global(pre) {
    background: #0f1117; border: 1px solid #2d3148;
    padding: 0.75rem 1rem; border-radius: 6px; overflow-x: auto; margin: 0.5em 0;
  }
  .md-content :global(pre code) { background: none; border: none; padding: 0; font-size: 0.85em; }
  .md-content :global(blockquote) {
    border-left: 3px solid #6366f1; margin: 0.5em 0; padding: 0.3em 0.8em; color: #94a3b8;
  }
  .md-content :global(hr) { border: none; border-top: 1px solid #2d3148; margin: 0.75em 0; }
  .md-content :global(a) { color: #818cf8; text-decoration: underline; }
  .md-content :global(table) { border-collapse: collapse; margin: 0.5em 0; width: 100%; }
  .md-content :global(th), .md-content :global(td) {
    border: 1px solid #2d3148; padding: 0.35em 0.6em; text-align: left;
  }
  .md-content :global(th) { background: #13161f; }
  .cursor {
    display: inline-block; width: 2px; height: 1em;
    background: #6366f1; vertical-align: text-bottom;
    animation: blink 1s step-end infinite; margin-left: 1px;
  }
  @keyframes blink { 50% { opacity: 0; } }

  /* ── Tool row ── */
  .msg-tool { display: flex; }
  .tool-row {
    background: #12151e; border: 1px solid #2d3148; border-radius: 8px;
    padding: 0.5rem 0.75rem; font-size: 0.8rem; font-family: monospace;
    max-width: 85%;
  }
  .tool-header { display: flex; align-items: center; gap: 0.5rem; color: #94a3b8; }
  .tool-name { color: #a5b4fc; font-weight: 600; }
  .tool-spinner {
    width: 10px; height: 10px; border: 2px solid #2d3148;
    border-top-color: #6366f1; border-radius: 50%;
    animation: spin .7s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  .tool-result { margin-top: 0.3rem; color: #64748b; white-space: pre-wrap; word-break: break-word; }
  .tool-result.error { color: #fca5a5; }
  .tool-params {
    margin-top: 0.3rem; color: #475569;
    white-space: pre-wrap; word-break: break-word; font-size: 0.75rem;
  }

  /* ── Compaction notice ── */
  .msg-compaction { display: flex; justify-content: center; }
  .compaction-notice {
    background: #1a1d27; border: 1px dashed #2d3148; border-radius: 6px;
    padding: 0.4rem 0.9rem; font-size: 0.78rem; color: #475569;
    text-align: center; max-width: 60%;
  }

  /* ── Error banner ── */
  .error-banner {
    background: #3b1515; border-top: 1px solid #7f1d1d;
    color: #fca5a5; font-size: 0.85rem; padding: 0.5rem 1.25rem; flex-shrink: 0;
  }

  /* ── Permission modal ── */
  .modal-backdrop {
    position: fixed; inset: 0; background: rgba(0,0,0,.6);
    display: flex; align-items: center; justify-content: center; z-index: 100;
  }
  .modal {
    background: #1a1d27; border: 1px solid #2d3148; border-radius: 12px;
    padding: 1.75rem; width: 480px; max-width: calc(100vw - 2rem);
  }
  .modal h2 { font-size: 1rem; font-weight: 600; margin-bottom: 0.35rem; }
  .modal .tool-label { color: #a5b4fc; font-family: monospace; font-size: 0.9rem; }
  .modal .desc { color: #94a3b8; font-size: 0.85rem; margin: 0.6rem 0; line-height: 1.5; }
  .modal pre {
    background: #12151e; border: 1px solid #2d3148; border-radius: 6px;
    padding: 0.6rem; font-size: 0.75rem; color: #64748b; overflow-x: auto;
    max-height: 180px; white-space: pre-wrap; word-break: break-word; margin-bottom: 1.25rem;
  }
  .modal-btns { display: flex; gap: 0.5rem; justify-content: flex-end; }
  .btn-deny {
    padding: 0.5rem 1.1rem; border-radius: 6px; font-size: 0.875rem; font-weight: 500;
    cursor: pointer; border: 1px solid #2d3148; background: transparent; color: #94a3b8;
  }
  .btn-deny:hover { background: #12151e; color: #e2e8f0; }
  .btn-approve {
    padding: 0.5rem 1.1rem; border-radius: 6px; font-size: 0.875rem; font-weight: 500;
    cursor: pointer; border: none; background: #6366f1; color: #fff;
  }
  .btn-approve:hover { background: #4f46e5; }

  /* ── Pending files ── */
  .pending-files {
    display: flex; flex-wrap: wrap; gap: 0.4rem;
    padding: 0.5rem 1.25rem 0;
    background: #1a1d27; flex-shrink: 0;
  }
  .file-pill {
    display: flex; align-items: center; gap: 0.35rem;
    background: #12151e; border: 1px solid #2d3148; border-radius: 5px;
    padding: 0.25rem 0.5rem; font-size: 0.75rem; color: #94a3b8;
  }
  .file-pill .fname { max-width: 120px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .file-pill .fsize { color: #475569; }
  .file-pill button {
    background: none; border: none; color: #475569; cursor: pointer;
    padding: 0; line-height: 1; font-size: 0.75rem;
  }
  .file-pill button:hover { color: #fca5a5; }

  /* ── Input bar ── */
  .input-bar {
    display: flex; gap: 0.5rem; padding: 0.75rem 1.25rem;
    background: #1a1d27; border-top: 1px solid #2d3148; flex-shrink: 0;
  }
  input[type=file] { display: none; }
  button.attach {
    background: none; border: 1px solid #2d3148; border-radius: 8px;
    color: #64748b; cursor: pointer; padding: 0 0.65rem;
    font-size: 1rem; height: 42px; transition: color 0.15s, border-color 0.15s;
    align-self: flex-end; flex-shrink: 0;
  }
  button.attach:hover:not(:disabled) { color: #a5b4fc; border-color: #6366f1; }
  button.attach:disabled { opacity: 0.4; cursor: not-allowed; }
  textarea {
    flex: 1; background: #0f1117; border: 1px solid #2d3148; border-radius: 8px;
    color: #e2e8f0; font: inherit; font-size: 0.925rem;
    padding: 0.6rem 0.8rem; resize: none;
    min-height: 42px; max-height: 180px; outline: none;
    transition: border-color .15s; line-height: 1.5;
  }
  textarea:focus { border-color: #6366f1; }
  button.send {
    background: #6366f1; color: #fff; border: none; border-radius: 8px;
    padding: 0 1.1rem; font-size: 0.9rem; font-weight: 500;
    cursor: pointer; transition: background .15s; align-self: flex-end; height: 42px;
  }
  button.send:hover:not(:disabled) { background: #4f46e5; }
  button.send:disabled { opacity: 0.4; cursor: not-allowed; }
</style>

<!-- Permission modal — blocks input until resolved -->
{#if permRequest}
  <div class="modal-backdrop">
    <div class="modal">
      <h2>Permission required</h2>
      <div class="tool-label">{permRequest.toolName}</div>
      {#if permRequest.description}
        <p class="desc">{permRequest.description}</p>
      {/if}
      {#if permRequest.params}
        <pre>{fmtParams(permRequest.params)}</pre>
      {/if}
      <div class="modal-btns">
        <button class="btn-deny"    onclick={() => respondPerm(false)}>Deny</button>
        <button class="btn-approve" onclick={() => respondPerm(true)}>Approve</button>
      </div>
    </div>
  </div>
{/if}

<div class="outer">
<div class="layout">
  <header>
    {#if sessionTitle}
      <span class="session-title">{sessionTitle}</span>
    {/if}
    <div class="header-right">
      <button class="gear" class:active={toolsOpen}
        onclick={() => { toolsOpen = !toolsOpen; if (toolsOpen) settingsOpen = false }}
        title="Tools">🔧</button>
      <button class="gear" class:active={settingsOpen}
        onclick={() => { settingsOpen = !settingsOpen; if (settingsOpen) toolsOpen = false }}
        title="Hyperparameters">⚙</button>
      <div class="dot" class:ok={connected}></div>
    </div>
  </header>

  <div class="messages">
    {#each messages as msg}

      {#if msg.kind === 'user'}
        <div class="msg-user">
          <div class="bubble-user">
            {#if msg.attachments?.length}
              <div class="hist-attachments">
                {#each msg.attachments as f}
                  <span class="hist-pill">📎 {f.name} <span class="hist-size">{f.size}</span></span>
                {/each}
              </div>
            {/if}
            {#if msg.content}{msg.content}{/if}
          </div>
        </div>

      {:else if msg.kind === 'assistant'}
        <div class="msg-assistant">
          {#if msg.thinking}
            <details class="thinking-box" ontoggle={e => { msg._open = e.target.open; messages = messages }}>
              <summary>Thinking{#if msg.streaming && !msg._open}<span class="thinking-live">{msg.thinking.split('\n').filter(l => l.trim()).slice(-3).join('\n')}</span>{/if}</summary>
              <div class="thinking-content">{msg.thinking}</div>
            </details>
          {/if}
          <div class="bubble-assistant md-content">
            {@html renderMarkdown(msg.content)}{#if msg.streaming}<span class="cursor"></span>{/if}
          </div>
        </div>

      {:else if msg.kind === 'tool'}
        <div class="msg-tool">
          <div class="tool-row">
            <div class="tool-header">
              <span class="tool-name">{msg.toolName}</span>
              {#if msg.pending}<div class="tool-spinner"></div>{/if}
            </div>
            {#if msg.params && msg.pending}
              <div class="tool-params">{fmtParams(msg.params)}</div>
            {/if}
            {#if msg.result !== null}
              <div class="tool-result" class:error={msg.isError}>{msg.result}</div>
            {/if}
          </div>
        </div>

      {:else if msg.kind === 'compaction'}
        <div class="msg-compaction">
          <div class="compaction-notice">
            Conversation compacted ({msg.before} → {msg.after} messages)
            {#if msg.summary} · {msg.summary}{/if}
          </div>
        </div>
      {/if}

    {/each}
    <div bind:this={msgEnd}></div>
  </div>

  {#if error}
    <div class="error-banner">{error}</div>
  {/if}

  {#if pendingFiles.length}
    <div class="pending-files">
      {#each pendingFiles as f (f.path)}
        <div class="file-pill">
          <span class="fname">{f.name}</span>
          <span class="fsize">{fmtSize(f.size)}</span>
          <button onclick={() => removeFile(f.path)}>✕</button>
        </div>
      {/each}
    </div>
  {/if}

  <div class="input-bar">
    <input
      type="file"
      multiple
      bind:this={fileInput}
      onchange={onFileSelected}
    />
    <button
      class="attach"
      onclick={() => fileInput.click()}
      disabled={!connected || !!permRequest || uploading}
      title="Attach files"
    >{uploading ? '⏳' : '📎'}</button>
    <textarea
      bind:value={input}
      onkeydown={onKeydown}
      placeholder={permRequest
        ? 'Waiting for permission response…'
        : connected
          ? 'Message…  (Enter to send, Shift+Enter for newline)'
          : 'Connecting…'}
      disabled={!connected || !!permRequest}
      rows="1"
    ></textarea>
    <button class="send" onclick={send}
      disabled={!connected || (!input.trim() && !pendingFiles.length) || !!permRequest}>
      Send
    </button>
  </div>
</div>

<SettingsPanel {sessionId} bind:open={settingsOpen} />
<ToolsPanel {sessionId} bind:open={toolsOpen} />

</div>
