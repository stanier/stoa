<script>
  import Toggle from './Toggle.svelte'

  let { allTools, saving, serverEnabled, toolEnabled, onToggleServer, onToggleTool,
        showBuiltin = false } = $props()

  let builtinTools = $derived(showBuiltin ? allTools.filter(t => t.source === 'builtin') : [])

  let collapsedServers = $state(new Set())

  $effect(() => {
    collapsedServers = new Set(
      allTools.filter(t => t.source !== 'builtin').map(t => t.source.replace(/^mcp_/, ''))
    )
  })

  let mcpServers = $derived(
    [...new Set(
      allTools
        .filter(t => t.source !== 'builtin')
        .map(t => t.source.replace(/^mcp_/, ''))
    )]
  )

  function toggleCollapse(server) {
    const next = new Set(collapsedServers)
    if (next.has(server)) next.delete(server)
    else next.add(server)
    collapsedServers = next
  }
</script>

{#each mcpServers as server}
  {@const serverTools = allTools.filter(t => t.source === 'mcp_' + server)}
  {@const isOpen = !collapsedServers.has(server)}
  <div class="server-section">
    <div class="server-header">
      <button class="chevron" class:open={isOpen}
        onclick={() => toggleCollapse(server)}
        title={isOpen ? 'Collapse' : 'Expand'}>▶</button>
      <span class="server-name" title={server}>{server}</span>
      <Toggle
        checked={serverEnabled(server)}
        disabled={saving}
        onchange={e => onToggleServer(server, e.target.checked)}
        title="Enable/disable all {server} tools"
      />
    </div>
    {#if isOpen}
      <div class="tool-rows">
        {#each serverTools as tool}
          {@const enabled = toolEnabled(tool.name)}
          <div class="tool-row">
            <div class="tool-info">
              <div class="tool-name" class:disabled={!enabled}>{tool.name}</div>
              {#if tool.description}
                <div class="tool-desc" class:disabled={!enabled}>{tool.description}</div>
              {/if}
            </div>
            <Toggle
              checked={enabled}
              disabled={saving}
              onchange={e => onToggleTool(tool.name, e.target.checked)}
            />
          </div>
        {/each}
      </div>
    {/if}
  </div>
{/each}

{#if builtinTools.length > 0}
  <div class="server-section">
    <div class="server-header builtin">
      <span class="server-name">Built-in</span>
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
        <Toggle checked disabled />
      </div>
    {/each}
  </div>
{/if}

<style>
  .server-section { border-bottom: 1px solid var(--surface-hi); }
  .server-section:last-child { border-bottom: none; }

  .server-header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 0.45rem 0; gap: 0.5rem;
  }

  button.chevron {
    background: none; border: none; color: var(--text4);
    cursor: pointer; font-size: 0.6rem; padding: 0.1rem 0.25rem;
    line-height: 1; flex-shrink: 0; transition: color 0.15s, transform 0.15s;
  }
  button.chevron:hover { color: var(--text2); }
  button.chevron.open { transform: rotate(90deg); }

  .server-name {
    font-size: 0.85rem; font-weight: 500; color: var(--text2); flex: 1;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }

  .tool-rows {
    padding-left: 0.75rem;
    border-left: 2px solid var(--surface-hi);
    margin: 0.25rem 0 0.5rem;
  }

  .tool-row {
    display: flex; align-items: flex-start; justify-content: space-between;
    padding: 0.3rem 0; gap: 0.5rem;
  }

  .tool-info { flex: 1; min-width: 0; }

  .tool-name {
    font-size: 0.78rem; color: var(--text3); font-family: monospace; word-break: break-all;
  }
  .tool-name.disabled { color: var(--text5); }

  .tool-desc { font-size: 0.7rem; color: var(--text4); margin-top: 0.1rem; line-height: 1.35; }
  .tool-desc.disabled { color: var(--text5); }

  .builtin-note {
    font-size: 0.7rem; color: var(--text5);
    padding: 0.2rem 0 0.4rem 1.5rem; font-style: italic;
  }
</style>
