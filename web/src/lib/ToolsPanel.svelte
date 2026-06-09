<script>
  import SidePanel from './SidePanel.svelte'
  import McpServerList from './McpServerList.svelte'
  import { apiFetch } from './utils.js'

  let { sessionId, open = $bindable(false) } = $props()

  let allTools     = $state([])
  let allowedTools = $state(null)
  let saving       = $state(false)

  $effect(() => {
    if (open && sessionId) loadTools()
  })

  async function loadTools() {
    try {
      const [toolsRes, sessionRes] = await Promise.all([
        fetch('/api/tools'),
        fetch(`/api/sessions/${sessionId}/tools`),
      ])
      if (toolsRes.ok) allTools = await toolsRes.json()
      if (sessionRes.ok) {
        const data = await sessionRes.json()
        allowedTools = data.allowed_tools?.length > 0 ? data.allowed_tools : null
      }
    } catch (_) {}
  }

  function toolEnabled(name) {
    if (allowedTools === null) return true
    return allowedTools.includes(name)
  }

  function serverEnabled(serverName) {
    const serverTools = allTools.filter(t => t.source === 'mcp_' + serverName)
    if (serverTools.length === 0) return true
    return serverTools.every(t => toolEnabled(t.name))
  }

  async function toggleServer(serverName, enabled) {
    const serverTools = allTools.filter(t => t.source === 'mcp_' + serverName)
    const names = serverTools.map(t => t.name)

    let next
    if (allowedTools === null) {
      if (!enabled) {
        next = allTools.filter(t => !names.includes(t.name)).map(t => t.name)
      } else {
        return
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

  async function toggleTool(toolName, enabled) {
    let next
    if (allowedTools === null) {
      if (!enabled) {
        next = allTools.filter(t => t.name !== toolName).map(t => t.name)
      } else {
        return
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

  async function applyAllowedTools(list) {
    const allNames = allTools.map(t => t.name)
    const allEnabled = allNames.every(n => list.includes(n))

    saving = true
    try {
      await apiFetch(`/api/sessions/${sessionId}/tools`, {
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
  .empty {
    padding: 1.5rem 0.75rem; font-size: 0.82rem;
    color: var(--text4); text-align: center;
  }

  .saving-bar {
    font-size: 0.7rem; color: var(--accent);
    padding: 0.3rem 0.75rem; text-align: right; flex-shrink: 0;
  }
</style>

<SidePanel title="Tools" bind:open>
  {#if saving}
    <div class="saving-bar">Saving…</div>
  {/if}

  {#if allTools.length === 0}
    <div class="empty">No tools available</div>
  {:else}
    <McpServerList
      {allTools}
      {saving}
      {serverEnabled}
      {toolEnabled}
      onToggleServer={toggleServer}
      onToggleTool={toggleTool}
      showBuiltin
    />
  {/if}
</SidePanel>
