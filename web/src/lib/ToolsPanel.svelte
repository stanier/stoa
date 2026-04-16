<script>
  import { onMount } from 'svelte'

  let { sessionId, open = $bindable(false) } = $props()

  let allTools         = $state([])   // [{ name, description, source }]
  let allowedTools     = $state(null) // null = all allowed; [] = all blocked; [...] = explicit list
  let saving           = $state(false)
  let collapsedServers = $state(new Set())

  $effect(() => {
    if (open && sessionId) {
      loadTools()
    }
  })

  async function loadTools() {
    try {
      const [toolsRes, sessionRes] = await Promise.all([
        fetch('/api/tools'),
        fetch(`/api/sessions/${sessionId}/tools`),
      ])
      if (toolsRes.ok) {
        allTools = await toolsRes.json()
        collapsedServers = new Set(
          allTools.filter(t => t.source !== 'builtin').map(t => t.source.replace(/^mcp_/, ''))
        )
      }
      if (sessionRes.ok) {
        const data = await sessionRes.json()
        allowedTools = data.allowed_tools?.length > 0 ? data.allowed_tools : null
      }
    } catch (_) {}
  }

  // Unique MCP server names (tools with source "mcp_<server>")
  let mcpServers = $derived(
    [...new Set(
      allTools
        .filter(t => t.source !== 'builtin')
        .map(t => t.source.replace(/^mcp_/, ''))
    )]
  )

  let builtinTools = $derived(allTools.filter(t => t.source === 'builtin'))

  // Determine if a tool is currently allowed.
  function toolEnabled(name) {
    if (allowedTools === null) return true
    return allowedTools.includes(name)
  }

  // Determine if all tools in a server are enabled.
  function serverEnabled(serverName) {
    const serverTools = allTools.filter(t => t.source === 'mcp_' + serverName)
    if (serverTools.length === 0) return true
    return serverTools.every(t => toolEnabled(t.name))
  }

  // Toggle an entire MCP server on/off.
  async function toggleServer(serverName, enabled) {
    const serverTools = allTools.filter(t => t.source === 'mcp_' + serverName)
    const names = serverTools.map(t => t.name)

    let next
    if (allowedTools === null) {
      if (!enabled) {
        // Start from "all allowed", exclude this server's tools.
        next = allTools.filter(t => !names.includes(t.name)).map(t => t.name)
      } else {
        return // already all enabled
      }
    } else {
      if (enabled) {
        next = [...new Set([...allowedTools, ...names])]
      } else {
        next = allowedTools.filter(n => !names.includes(n))
      }
    }

    await applyAllowedTools(next)
  }

  // Toggle a single MCP tool on/off.
  async function toggleTool(toolName, enabled) {
    let next
    if (allowedTools === null) {
      if (!enabled) {
        next = allTools.filter(t => t.name !== toolName).map(t => t.name)
      } else {
        return // already enabled
      }
    } else {
      if (enabled) {
        next = [...new Set([...allowedTools, toolName])]
      } else {
        next = allowedTools.filter(n => n !== toolName)
      }
    }

    await applyAllowedTools(next)
  }

  function toggleCollapse(server) {
    const next = new Set(collapsedServers)
    if (next.has(server)) next.delete(server)
    else next.add(server)
    collapsedServers = next
  }

  async function applyAllowedTools(list) {
    // If the list equals "all tools", normalise to null (no filter).
    const allNames = allTools.map(t => t.name)
    const allEnabled = allNames.every(n => list.includes(n))

    saving = true
    try {
      await fetch(`/api/sessions/${sessionId}/tools`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ allowed_tools: allEnabled ? [] : list }),
      })
      allowedTools = allEnabled ? null : list
    } catch (_) {}
    saving = false
  }
</script>

<style>
  .panel {
    width: 280px;
    flex-shrink: 0;
    background: #13161f;
    border-left: 1px solid #2d3148;
    display: flex;
    flex-direction: column;
    overflow-y: auto;
  }

  .panel-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.65rem 0.75rem;
    border-bottom: 1px solid #2d3148;
    flex-shrink: 0;
  }

  .panel-title {
    font-size: 0.72rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: #475569;
  }

  button.close {
    background: none;
    border: none;
    color: #475569;
    cursor: pointer;
    font-size: 0.9rem;
    padding: 0.1rem 0.3rem;
    transition: color 0.15s;
  }
  button.close:hover { color: #e2e8f0; }

  .section {
    border-bottom: 1px solid #1e2235;
    padding: 0.5rem 0;
  }

  .section-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.35rem 0.75rem;
    gap: 0.5rem;
  }

  .section-name {
    font-size: 0.75rem;
    font-weight: 600;
    color: #64748b;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  button.chevron {
    background: none;
    border: none;
    color: #475569;
    cursor: pointer;
    font-size: 0.65rem;
    padding: 0.1rem 0.25rem;
    line-height: 1;
    transition: color 0.15s, transform 0.15s;
    flex-shrink: 0;
  }
  button.chevron:hover { color: #94a3b8; }
  button.chevron.open { transform: rotate(90deg); }

  .tool-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    padding: 0.3rem 0.75rem 0.3rem 1.25rem;
    gap: 0.5rem;
  }

  .tool-info {
    flex: 1;
    min-width: 0;
  }

  .tool-name {
    font-size: 0.78rem;
    color: #94a3b8;
    font-family: monospace;
    word-break: break-all;
  }

  .tool-desc {
    font-size: 0.7rem;
    color: #475569;
    margin-top: 0.1rem;
    line-height: 1.35;
  }

  .tool-name.disabled { color: #3b4361; }
  .tool-desc.disabled { color: #2d3148; }

  /* Toggle switch */
  .toggle {
    position: relative;
    flex-shrink: 0;
    width: 30px;
    height: 16px;
    margin-top: 2px;
  }
  .toggle input { opacity: 0; width: 0; height: 0; }
  .toggle-track {
    position: absolute;
    inset: 0;
    background: #2d3148;
    border-radius: 8px;
    cursor: pointer;
    transition: background 0.2s;
  }
  .toggle input:checked + .toggle-track { background: #6366f1; }
  .toggle-track::after {
    content: '';
    position: absolute;
    left: 2px; top: 2px;
    width: 12px; height: 12px;
    background: #e2e8f0;
    border-radius: 50%;
    transition: transform 0.2s;
  }
  .toggle input:checked + .toggle-track::after { transform: translateX(14px); }
  .toggle input:disabled + .toggle-track { opacity: 0.4; cursor: not-allowed; }

  .builtin-note {
    font-size: 0.7rem;
    color: #334155;
    padding: 0.4rem 0.75rem;
    font-style: italic;
  }

  .empty {
    padding: 1.5rem 0.75rem;
    font-size: 0.82rem;
    color: #475569;
    text-align: center;
  }

  .saving-bar {
    font-size: 0.7rem;
    color: #6366f1;
    padding: 0.3rem 0.75rem;
    text-align: right;
    flex-shrink: 0;
  }
</style>

{#if open}
<div class="panel">
  <div class="panel-header">
    <span class="panel-title">Tools</span>
    <button class="close" onclick={() => open = false}>✕</button>
  </div>

  {#if saving}
    <div class="saving-bar">Saving…</div>
  {/if}

  {#if allTools.length === 0}
    <div class="empty">No tools available</div>
  {:else}

    <!-- MCP server sections -->
    {#each mcpServers as server}
      {@const serverTools = allTools.filter(t => t.source === 'mcp_' + server)}
      {@const isOpen = !collapsedServers.has(server)}
      <div class="section">
        <div class="section-header">
          <button class="chevron" class:open={isOpen}
            onclick={() => toggleCollapse(server)}
            title={isOpen ? 'Collapse' : 'Expand'}>▶</button>
          <span class="section-name" title={server}>{server}</span>
          <label class="toggle" title="Enable/disable all {server} tools">
            <input type="checkbox"
              checked={serverEnabled(server)}
              disabled={saving}
              onchange={e => toggleServer(server, e.target.checked)} />
            <span class="toggle-track"></span>
          </label>
        </div>
        {#if isOpen}
          {#each serverTools as tool}
            {@const enabled = toolEnabled(tool.name)}
            <div class="tool-row">
              <div class="tool-info">
                <div class="tool-name" class:disabled={!enabled}>{tool.name}</div>
                {#if tool.description}
                  <div class="tool-desc" class:disabled={!enabled}>{tool.description}</div>
                {/if}
              </div>
              <label class="toggle" title={enabled ? 'Disable tool' : 'Enable tool'}>
                <input type="checkbox"
                  checked={enabled}
                  disabled={saving}
                  onchange={e => toggleTool(tool.name, e.target.checked)} />
                <span class="toggle-track"></span>
              </label>
            </div>
          {/each}
        {/if}
      </div>
    {/each}

    <!-- Built-in tools (read-only) -->
    {#if builtinTools.length > 0}
      <div class="section">
        <div class="section-header">
          <span class="section-name">Built-in</span>
        </div>
        <div class="builtin-note">Built-in tools are always enabled</div>
        {#each builtinTools as tool}
          <div class="tool-row">
            <div class="tool-info">
              <div class="tool-name">{tool.name}</div>
              {#if tool.description}
                <div class="tool-desc">{tool.description}</div>
              {/if}
            </div>
            <label class="toggle">
              <input type="checkbox" checked disabled />
              <span class="toggle-track"></span>
            </label>
          </div>
        {/each}
      </div>
    {/if}

  {/if}
</div>
{/if}
