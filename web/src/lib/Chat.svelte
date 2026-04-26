<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import SettingsPanel from './SettingsPanel.svelte'
  import ToolsPanel from './ToolsPanel.svelte'
  import ModelPicker from './ModelPicker.svelte'
  import { marked } from 'marked'
  import hljs from 'highlight.js/lib/core'
  import langBash       from 'highlight.js/lib/languages/bash'
  import langC          from 'highlight.js/lib/languages/c'
  import langCpp        from 'highlight.js/lib/languages/cpp'
  import langCSS        from 'highlight.js/lib/languages/css'
  import langDockerfile from 'highlight.js/lib/languages/dockerfile'
  import langGo         from 'highlight.js/lib/languages/go'
  import langHtml       from 'highlight.js/lib/languages/xml'
  import langJS         from 'highlight.js/lib/languages/javascript'
  import langJSON       from 'highlight.js/lib/languages/json'
  import langMarkdown   from 'highlight.js/lib/languages/markdown'
  import langPython     from 'highlight.js/lib/languages/python'
  import langRust       from 'highlight.js/lib/languages/rust'
  import langSQL        from 'highlight.js/lib/languages/sql'
  import langTS         from 'highlight.js/lib/languages/typescript'
  import langYAML       from 'highlight.js/lib/languages/yaml'

  hljs.registerLanguage('bash',       langBash)
  hljs.registerLanguage('sh',         langBash)
  hljs.registerLanguage('shell',      langBash)
  hljs.registerLanguage('c',          langC)
  hljs.registerLanguage('cpp',        langCpp)
  hljs.registerLanguage('css',        langCSS)
  hljs.registerLanguage('dockerfile', langDockerfile)
  hljs.registerLanguage('go',         langGo)
  hljs.registerLanguage('html',       langHtml)
  hljs.registerLanguage('xml',        langHtml)
  hljs.registerLanguage('javascript', langJS)
  hljs.registerLanguage('js',         langJS)
  hljs.registerLanguage('json',       langJSON)
  hljs.registerLanguage('markdown',   langMarkdown)
  hljs.registerLanguage('python',     langPython)
  hljs.registerLanguage('py',         langPython)
  hljs.registerLanguage('rust',       langRust)
  hljs.registerLanguage('sql',        langSQL)
  hljs.registerLanguage('typescript', langTS)
  hljs.registerLanguage('ts',         langTS)
  hljs.registerLanguage('yaml',       langYAML)
  hljs.registerLanguage('yml',        langYAML)

  marked.use({
    renderer: {
      code({ text, lang }) {
        const language = lang && hljs.getLanguage(lang) ? lang : null
        let highlighted
        try {
          highlighted = language
            ? hljs.highlight(text, { language }).value
            : hljs.highlightAuto(text).value
        } catch (_) {
          highlighted = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
        }
        const bar = language
          ? `<div class="hljs-lang-bar">${language}</div>`
          : ''
        return `<div class="hljs-block">${bar}<pre><code class="hljs">${highlighted}</code></pre></div>`
      },
      codespan({ text }) {
        return `<code class="inline-code">${text}</code>`
      },
    },
  })

  function renderMarkdown(text) {
    return marked.parse(text ?? '', { breaks: true })
  }

  // ── AssistantMeta helpers ────────────────────────────────────────────────
  let copyStates = $state({})  // toolUseId-ish key → 'copied'

  function fmtTime(iso) {
    if (!iso) return null
    return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }

  function tokPerSec(msg) {
    if (!msg.tokens || !msg.duration) return null
    return (msg.tokens / msg.duration).toFixed(1)
  }

  function copyMsg(msg, key) {
    navigator.clipboard?.writeText(msg.content).then(() => {
      copyStates = { ...copyStates, [key]: true }
      setTimeout(() => { copyStates = { ...copyStates, [key]: false } }, 1800)
    })
  }

  let { user, sessionId, sessionTitle = '', onSessionEnd, onTitleUpdate } = $props()

  let liveTitle = $state(sessionTitle)
  $effect(() => { liveTitle = sessionTitle })

  let settingsOpen  = $state(false)
  let toolsOpen     = $state(false)
  let messages      = $state([])
  let input         = $state('')
  let connected     = $state(false)
  let error         = $state(null)
  let permRequest   = $state(null)
  let pendingFiles  = $state([])
  let uploading     = $state(false)
  let fileInput     = null
  let ws            = null
  let msgEnd        = null

  onMount(async () => {
    await loadHistory()
    openWS()
  })

  // S3 mode: agent calls read_attachment(attachment_id); local fallback uses filesystem path.
  const ATTACH_PREFIX_ID   = '[Attachments — use read_attachment to read them]\n'
  const ATTACH_PREFIX_PATH = '[Attachments — use mcp_filesystem_read_file to read them]\n'

  function parseUserContent(content) {
    const isId   = content.startsWith(ATTACH_PREFIX_ID)
    const isPath = content.startsWith(ATTACH_PREFIX_PATH)
    if (!isId && !isPath) return { text: content, attachments: [] }

    const prefix = isId ? ATTACH_PREFIX_ID : ATTACH_PREFIX_PATH
    const rest = content.slice(prefix.length)
    const lines = rest.split('\n')
    const attachments = []
    let textStart = 0
    for (let i = 0; i < lines.length; i++) {
      const m = lines[i].match(/^- (.+?) \((attachment|path): (.+?), size: (.+?)\)$/)
      if (m) {
        attachments.push({
          name:         m[1],
          attachmentId: m[2] === 'attachment' ? m[3] : undefined,
          path:         m[2] === 'path'       ? m[3] : undefined,
          size:         m[4],
        })
        textStart = i + 1
      } else {
        break
      }
    }
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
            return [{ kind: 'assistant', content: m.Content, thinking: m.Thinking ?? '', streaming: false,
              timestamp: m.CreatedAt ?? null, tokens: m.OutputTokens ?? null }]
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
            kind: 'assistant', content, thinking, streaming: true,
            streamStart: Date.now(),
            timestamp: new Date().toISOString(),
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
          if (ev.done?.output_tokens) last.tokens = ev.done.output_tokens
          if (last.streamStart) last.duration = (Date.now() - last.streamStart) / 1000
          last.streaming = false
          messages = messages
        }
        scrollToBottom()
        break
      }

      case 'title': {
        if (ev.title?.title) {
          liveTitle = ev.title.title
          onTitleUpdate?.(sessionId, ev.title.title)
        }
        break
      }

      case 'tool_invoked': {
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
        pendingFiles = [...pendingFiles, { name: data.name, attachmentId: data.attachment_id, path: data.path, size: data.size }]
      } catch (e) {
        error = `Upload error: ${file.name}`
      }
    }
    uploading = false
  }

  function removeFile(f) {
    const key = f.attachmentId || f.path
    pendingFiles = pendingFiles.filter(pf => (pf.attachmentId || pf.path) !== key)
  }

  function fmtSize(bytes) {
    if (bytes < 1024) return `${bytes} B`
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  }

  function send() {
    const text = input.trim()
    if ((!text && !pendingFiles.length) || !connected || permRequest) return
    input = ''

    let content = text
    let sentAttachments = []
    if (pendingFiles.length) {
      sentAttachments = [...pendingFiles]
      const hasId = pendingFiles.some(f => f.attachmentId)
      const prefix = hasId ? ATTACH_PREFIX_ID : ATTACH_PREFIX_PATH
      const refs = pendingFiles.map(f => {
        const refType = f.attachmentId ? 'attachment' : 'path'
        const ref     = f.attachmentId || f.path
        return `- ${f.name} (${refType}: ${ref}, size: ${fmtSize(f.size)})`
      }).join('\n')
      content = text ? `${prefix}${refs}\n\n${text}` : `${prefix}${refs}`
      pendingFiles = []
    }

    messages = [...messages, { kind: 'user', content: text || '', attachments: sentAttachments.map(f => ({ name: f.name, attachmentId: f.attachmentId, path: f.path, size: fmtSize(f.size) })) }]
    scrollToBottom()
    ws.send(JSON.stringify({ type: 'send_message', send_message: { content } }))
    onSessionEnd?.()
  }

  function onKeydown(e) {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
  }

  function respondPerm(approved) {
    if (!permRequest) return
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
  .outer  { display: flex; flex: 1; min-width: 0; overflow: hidden; }
  .layout { display: flex; flex-direction: column; flex: 1; min-width: 0; overflow: hidden; }

  .panel-slot {
    position: relative;
    flex-shrink: 0;
    overflow: hidden;
    transition: width 0.2s ease;
  }
  .panel-overlay {
    position: absolute;
    inset: 0;
    height: 100%;
    pointer-events: none;
    opacity: 0;
    transition: opacity 0.08s ease;
  }
  .panel-overlay.panel-visible {
    pointer-events: auto;
    opacity: 1;
    transition: opacity 0.15s ease 0.08s;
  }

  header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 0.75rem 1.25rem;
    background: var(--surface); flex-shrink: 0;
  }
  .session-title {
    font-weight: 600; font-size: 1rem; color: var(--text);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .header-right {
    display: flex; align-items: center; gap: 0.75rem;
    font-size: 0.85rem; color: var(--text4); margin-left: auto;
  }
  button.icon-btn {
    background: none; border: none; color: var(--text4); cursor: pointer;
    padding: 0.25rem; line-height: 1; display: flex; align-items: center;
    border-radius: 5px; transition: color 0.15s, background 0.15s;
  }
  button.icon-btn:hover, button.icon-btn.active {
    color: var(--accent-light); background: var(--surface-hi);
  }

  .messages {
    flex: 1; overflow-y: auto;
    padding: 1.25rem 10rem;
    display: flex; flex-direction: column; gap: 12px;
  }

  .msg-user { display: flex; justify-content: flex-end; }
  .bubble-user {
    background: var(--bubble-user); color: #fff;
    padding: 0.6rem 0.9rem; border-radius: 8px 8px 2px 8px;
    max-width: 70%; white-space: pre-wrap; word-break: break-word;
    font-size: 0.925rem; line-height: 1.6;
  }

  .hist-attachments { display: flex; flex-wrap: wrap; gap: 0.3rem; margin-bottom: 0.4rem; }
  .hist-pill {
    background: rgba(255,255,255,0.12); border-radius: 4px;
    padding: 0.15rem 0.45rem; font-size: 0.75rem;
  }
  .hist-size { opacity: 0.7; }

  .msg-assistant { display: flex; flex-direction: column; gap: 0.4rem; max-width: 85%; }

  .thinking-box {
    background: var(--depth); border-radius: 6px;
    padding: 0.5rem 0.75rem; font-size: 0.8rem; color: var(--text3);
    font-family: monospace; white-space: pre-wrap; word-break: break-word;
  }
  .thinking-box summary {
    cursor: pointer; color: var(--text3); font-family: inherit;
    list-style: none; display: flex; align-items: center; gap: 0.4rem;
    flex-wrap: wrap;
  }
  .thinking-box summary::before { flex-shrink: 0; }
  .thinking-box summary::before { content: '▶'; font-size: 0.6rem; transition: transform .2s; }
  details[open] summary::before { transform: rotate(90deg); }
  .thinking-content { margin-top: 0.4rem; }
  .thinking-live {
    display: block; width: 100%; margin-top: 0.25rem;
    color: var(--text4); font-size: 0.75rem; font-family: monospace;
    white-space: pre-wrap; word-break: break-word;
    opacity: 0.85; max-height: 3.6em; overflow: hidden;
  }

  .bubble-assistant {
    background: var(--surface-hi);
    padding: 0.6rem 0.9rem; border-radius: 8px 8px 8px 2px;
    word-break: break-word; font-size: 0.925rem; line-height: 1.6;
  }
  .md-content :global(p)  { margin: 0 0 0.6em; }
  .md-content :global(p:last-child) { margin-bottom: 0; }
  .md-content :global(h1), .md-content :global(h2), .md-content :global(h3),
  .md-content :global(h4), .md-content :global(h5), .md-content :global(h6) {
    margin: 0.8em 0 0.3em; font-weight: 600; line-height: 1.3;
  }
  .md-content :global(ul), .md-content :global(ol) { margin: 0.3em 0 0.6em 1.4em; padding: 0; }
  .md-content :global(li) { margin: 0.15em 0; }
  .md-content :global(.inline-code) {
    background: var(--bg); border: 1px solid var(--border);
    padding: 0.1em 0.35em; border-radius: 4px; font-size: 0.875em; font-family: monospace;
  }
  /* highlight.js code blocks */
  .md-content :global(.hljs-block) {
    border: 1px solid var(--border); border-radius: 7px;
    overflow: hidden; font-size: 0.82em; margin: 0.5em 0;
  }
  .md-content :global(.hljs-lang-bar) {
    background: #161b22; padding: 0.3rem 0.9rem;
    font-size: 0.72em; color: #8b949e; font-family: monospace;
    border-bottom: 1px solid #30363d; letter-spacing: 0.04em;
  }
  .md-content :global(.hljs-block pre) {
    margin: 0; padding: 0.75rem 1rem; overflow-x: auto;
    background: #0d1117; font-family: monospace; font-size: 1em;
  }
  .md-content :global(.hljs-block pre code) {
    background: none; border: none; padding: 0; font-size: 1em;
  }
  /* Fallback for non-hljs-block code (shouldn't appear but just in case) */
  .md-content :global(code) {
    background: var(--bg); border: 1px solid var(--border);
    padding: 0.1em 0.35em; border-radius: 4px; font-size: 0.875em; font-family: monospace;
  }
  .md-content :global(pre) {
    background: var(--depth); border: 1px solid var(--border);
    padding: 0.75rem 1rem; border-radius: 6px; overflow-x: auto; margin: 0.5em 0;
  }
  .md-content :global(pre code) { background: none; border: none; padding: 0; font-size: 0.85em; }
  .md-content :global(blockquote) {
    border-left: 3px solid var(--accent); margin: 0.5em 0;
    padding: 0.3em 0.8em; color: var(--text2);
  }
  .md-content :global(hr) { border: none; border-top: 1px solid var(--border); margin: 0.75em 0; }
  .md-content :global(a) { color: var(--accent-light); text-decoration: underline; }
  .md-content :global(table) { border-collapse: collapse; margin: 0.5em 0; width: 100%; }
  .md-content :global(th), .md-content :global(td) {
    border: 1px solid var(--border); padding: 0.35em 0.6em; text-align: left;
  }
  .md-content :global(th) { background: var(--sidebar); }
  .cursor {
    display: inline-block; width: 2px; height: 1em;
    background: var(--accent); vertical-align: text-bottom;
    animation: blink 1s step-end infinite; margin-left: 1px;
  }
  @keyframes blink { 50% { opacity: 0; } }

  /* Assistant metadata bar */
  .msg-meta {
    display: flex; align-items: center; gap: 0.6rem;
    padding: 0 0.1rem; height: 22px;
  }
  .meta-time {
    font-size: 0.7rem; color: var(--text5);
    font-variant-numeric: tabular-nums;
  }
  .meta-tps {
    font-size: 0.7rem; color: var(--text5);
    font-variant-numeric: tabular-nums;
    display: flex; align-items: center; gap: 0.2rem;
    cursor: default;
  }
  .meta-spacer { flex: 1; }
  .meta-btn {
    background: none; border: none; cursor: pointer;
    padding: 0.1rem 0.3rem; border-radius: 4px;
    font-size: 0.7rem; color: var(--text5);
    display: flex; align-items: center; gap: 0.25rem;
    transition: color 0.15s; font-family: inherit;
  }
  .meta-btn:hover { color: var(--text2); }
  .meta-btn.copied { color: var(--accent-light); }

  .msg-tool { display: flex; }
  .tool-row {
    background: var(--depth); border-radius: 8px;
    padding: 0.5rem 0.75rem; font-size: 0.8rem; font-family: monospace;
    max-width: 85%;
  }
  .tool-header { display: flex; align-items: center; gap: 0.5rem; color: var(--text3); }
  .tool-name { color: var(--accent-light); font-weight: 600; }
  .tool-spinner {
    width: 10px; height: 10px; border: 2px solid var(--border);
    border-top-color: var(--accent); border-radius: 50%;
    animation: spin .7s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  .tool-result { margin-top: 0.3rem; color: var(--text3); white-space: pre-wrap; word-break: break-word; }
  .tool-result.error { color: #fca5a5; }
  .tool-params { margin-top: 0.3rem; color: var(--text4); white-space: pre-wrap; word-break: break-word; font-size: 0.75rem; }

  .msg-compaction { display: flex; justify-content: center; }
  .compaction-notice {
    background: var(--surface); border: 1px dashed var(--border); border-radius: 6px;
    padding: 0.4rem 0.9rem; font-size: 0.78rem; color: var(--text4);
    text-align: center; max-width: 60%;
  }

  .error-banner {
    background: #3b1515; border-top: 1px solid #7f1d1d;
    color: #fca5a5; font-size: 0.85rem; padding: 0.5rem 1.25rem; flex-shrink: 0;
  }

  .modal-backdrop {
    position: fixed; inset: 0; background: rgba(0,0,0,.6);
    display: flex; align-items: center; justify-content: center; z-index: 100;
  }
  .modal {
    background: var(--surface); border: 1px solid var(--border); border-radius: 12px;
    padding: 1.75rem; width: 480px; max-width: calc(100vw - 2rem);
  }
  .modal h2 { font-size: 1rem; font-weight: 600; margin-bottom: 0.35rem; color: var(--text); }
  .modal .tool-label { color: var(--accent-light); font-family: monospace; font-size: 0.9rem; }
  .modal .desc { color: var(--text2); font-size: 0.85rem; margin: 0.6rem 0; line-height: 1.5; }
  .modal pre {
    background: var(--depth); border: 1px solid var(--border); border-radius: 6px;
    padding: 0.6rem; font-size: 0.75rem; color: var(--text3); overflow-x: auto;
    max-height: 180px; white-space: pre-wrap; word-break: break-word; margin-bottom: 1.25rem;
  }
  .modal-btns { display: flex; gap: 0.5rem; justify-content: flex-end; }
  .btn-deny {
    padding: 0.5rem 1.1rem; border-radius: 6px; font-size: 0.875rem; font-weight: 500;
    cursor: pointer; border: 1px solid var(--border); background: transparent; color: var(--text2);
  }
  .btn-deny:hover { background: var(--depth); color: var(--text); }
  .btn-approve {
    padding: 0.5rem 1.1rem; border-radius: 6px; font-size: 0.875rem; font-weight: 500;
    cursor: pointer; border: none; background: var(--accent); color: var(--bg);
  }
  .btn-approve:hover { background: var(--accent-light); }

  .pending-files {
    display: flex; flex-wrap: wrap; gap: 0.4rem;
    padding: 0.5rem 1.25rem 0;
    background: var(--surface); flex-shrink: 0;
  }
  .file-pill {
    display: flex; align-items: center; gap: 0.35rem;
    background: var(--depth); border: 1px solid var(--border); border-radius: 5px;
    padding: 0.25rem 0.5rem; font-size: 0.75rem; color: var(--text2);
  }
  .file-pill .fname { max-width: 120px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .file-pill .fsize { color: var(--text4); }
  .file-pill button {
    background: none; border: none; color: var(--text4); cursor: pointer;
    padding: 0; line-height: 1; font-size: 0.75rem;
  }
  .file-pill button:hover { color: #fca5a5; }

  .input-bar {
    display: flex; gap: 0.5rem; padding: 0.75rem 1.25rem;
    background: var(--surface); flex-shrink: 0;
  }
  input[type=file] { display: none; }
  button.attach {
    background: none; border: 1px solid var(--border); border-radius: 8px;
    color: var(--text3); cursor: pointer; padding: 0 0.65rem;
    height: 42px; transition: color 0.15s, border-color 0.15s;
    align-self: flex-end; flex-shrink: 0;
    display: flex; align-items: center; justify-content: center;
  }
  button.attach:hover:not(:disabled) { color: var(--accent-light); border-color: var(--accent); }
  button.attach:disabled { opacity: 0.4; cursor: not-allowed; }
  textarea {
    flex: 1; background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); font: inherit; font-size: 0.925rem;
    padding: 0.6rem 0.8rem; resize: none;
    min-height: 42px; max-height: 180px; outline: none;
    transition: border-color .15s; line-height: 1.5;
  }
  textarea:focus { border-color: var(--accent); }
  button.send {
    background: var(--accent); color: var(--bg); border: none; border-radius: 8px;
    padding: 0 1.1rem; font-size: 0.9rem; font-weight: 500;
    cursor: pointer; transition: background .15s; align-self: flex-end; height: 42px;
  }
  button.send:hover:not(:disabled) { background: var(--accent-light); }
  button.send:disabled { opacity: 0.4; cursor: not-allowed; }
</style>

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
    {#if liveTitle}
      <span class="session-title">{liveTitle}</span>
    {/if}
    <div class="header-right">
      <ModelPicker {sessionId} />
      <button class="icon-btn" class:active={toolsOpen}
        onclick={() => { toolsOpen = !toolsOpen; if (toolsOpen) settingsOpen = false }}
        title="Tools">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>
        </svg>
      </button>
      <button class="icon-btn" class:active={settingsOpen}
        onclick={() => { settingsOpen = !settingsOpen; if (settingsOpen) toolsOpen = false }}
        title="Settings">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        </svg>
      </button>
    </div>
  </header>

  <div class="messages">
    {#each messages as msg, i}

      {#if msg.kind === 'user'}
        <div class="msg-user">
          <div class="bubble-user">
            {#if msg.attachments?.length}
              <div class="hist-attachments">
                {#each msg.attachments as f}
                  <span class="hist-pill">
                    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="display:inline;vertical-align:middle;margin-right:3px"><path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/></svg>
                    {f.name} <span class="hist-size">{f.size}</span>
                  </span>
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
          {#if !msg.streaming}
            {@const msgKey = i}
            {@const tps = tokPerSec(msg)}
            <div class="msg-meta">
              {#if msg.timestamp}
                <span class="meta-time">{fmtTime(msg.timestamp)}</span>
              {/if}
              {#if tps}
                <span class="meta-tps" title="{msg.tokens} tokens in {msg.duration?.toFixed(1)}s">
                  <svg width="9" height="9" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
                    <path d="M6 1v5l3 2"/><circle cx="6" cy="6" r="5"/>
                  </svg>
                  {tps} tok/s
                </span>
              {/if}
              <span class="meta-spacer"></span>
              <button
                class="meta-btn"
                class:copied={copyStates[msgKey]}
                onclick={() => copyMsg(msg, msgKey)}
                title="Copy response"
              >
                {#if copyStates[msgKey]}
                  <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M2 6l3 3 5-5"/></svg>
                  Copied
                {:else}
                  <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="7" height="7" rx="1.2"/><path d="M1 8V2a1 1 0 0 1 1-1h6"/></svg>
                  Copy
                {/if}
              </button>
            </div>
          {/if}
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
      {#each pendingFiles as f ((f.attachmentId || f.path))}
        <div class="file-pill">
          <span class="fname">{f.name}</span>
          <span class="fsize">{fmtSize(f.size)}</span>
          <button onclick={() => removeFile(f)}>✕</button>
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
    >
      {#if uploading}
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83"/></svg>
      {:else}
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/></svg>
      {/if}
    </button>
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

<div class="panel-slot" style="width: {settingsOpen ? '260px' : toolsOpen ? '280px' : '0px'}">
  <div class="panel-overlay" class:panel-visible={settingsOpen}>
    <SettingsPanel {sessionId} bind:open={settingsOpen} />
  </div>
  <div class="panel-overlay" class:panel-visible={toolsOpen}>
    <ToolsPanel {sessionId} bind:open={toolsOpen} />
  </div>
</div>

</div>
