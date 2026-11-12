<script>
  // Decision graphs: build a small graph of questions for a decision model
  // (e.g. Liquid d1), then run it on a situation one step at a time or to
  // the end. Each step asks one question; the answer picks the next step.
  // When the model is unsure (below a step's confidence floor) the run
  // pauses so you can pick the branch yourself, and you can take a
  // different branch at the latest step any time.
  //
  // Graphs are kept in this browser's localStorage for now.
  import { onMount } from 'svelte'
  import PageLayout from './PageLayout.svelte'
  import DecisionResult from './DecisionResult.svelte'
  import { apiFetch } from './utils.js'
  import { QUESTION_TYPES, rowOutcomes, summarize, pct, toApiState } from './decision/form.js'
  import {
    MAX_NODES, blankGraph, blankNode, exampleGraph, nodeTitle, removeNode, toApiGraph,
    layout, newRun, applyTrace, overrideLast, stepOutcome, loadGraphs, saveGraphs,
  } from './decision/graph.js'

  let { onBack } = $props()

  let graphs    = $state([])
  let activeId  = $state(null)
  let models    = $state([])   // [{ provider, model }]
  let modelKey  = $state('')
  let modelsLoaded = $state(false)
  let stateText = $state('')
  let run       = $state(null)
  let busy      = $state(false)
  let error     = $state(null)
  let ready     = $state(false)

  let active = $derived(graphs.find(g => g.id === activeId) ?? null)
  let picked = $derived(models.find(m => key(m) === modelKey) ?? null)
  // Waiting for a person to pick the branch at a low-confidence step.
  let paused = $derived(run?.done === 'low_confidence')

  const key = m => `${m.provider}\u0000${m.model}`

  onMount(() => {
    const saved = loadGraphs(globalThis.localStorage)
    graphs = saved.length ? saved : [exampleGraph()]
    activeId = graphs[0].id
    ready = true
    loadModels()
  })

  // Save on every edit once loaded.
  $effect(() => {
    const snapshot = $state.snapshot(graphs)
    if (ready) saveGraphs(globalThis.localStorage, snapshot)
  })

  async function loadModels() {
    try {
      const res = await fetch('/api/models')
      if (res.ok) {
        const data = await res.json()
        const out = []
        for (const p of data.providers ?? []) {
          for (const [model, caps] of Object.entries(p.capabilities ?? {})) {
            if (caps.includes('decision')) out.push({ provider: p.name, model })
          }
        }
        models = out
        if (!out.some(m => key(m) === modelKey)) modelKey = out[0] ? key(out[0]) : ''
      }
    } catch (_) {}
    modelsLoaded = true
  }

  // ── Editing ────────────────────────────────────────────────────────────────
  function newGraph() {
    const g = blankGraph(`Graph ${graphs.length + 1}`)
    graphs = [...graphs, g]
    selectGraph(g.id)
  }

  function selectGraph(id) {
    activeId = id
    run = null
    error = null
  }

  function deleteGraph() {
    if (!active) return
    if (!confirm(`Delete "${active.name}"?`)) return
    graphs = graphs.filter(g => g.id !== active.id)
    if (!graphs.length) graphs = [blankGraph()]
    selectGraph(graphs[0].id)
  }

  function addNode() {
    if (!active || active.nodes.length >= MAX_NODES) return null
    const n = blankNode(active)
    active.nodes.push(n)
    if (!active.root) active.root = n.id
    return n.id
  }

  function deleteNode(id) {
    const i = graphs.findIndex(g => g.id === activeId)
    graphs[i] = removeNode($state.snapshot(graphs[i]), id)
    if (run && (run.current === id || run.steps.some(s => s.node === id))) run = null
  }

  function setEdge(node, outcome, value) {
    if (value === '__new') {
      const id = addNode()
      if (id) node.edges[outcome] = id
    } else if (value) {
      node.edges[outcome] = value
    } else {
      delete node.edges[outcome]
    }
  }

  // ── Running ────────────────────────────────────────────────────────────────
  function resetRun() {
    run = active ? newRun(active) : null
    error = null
  }

  async function advance(maxSteps) {
    if (!active || !picked) return
    error = null
    let graph, apiState
    try {
      graph = toApiGraph($state.snapshot(active))
      apiState = toApiState(stateText)
    } catch (e) {
      error = e.message
      return
    }
    if (!run || (run.done && run.steps.length)) run = newRun(active)
    if (!run.current) return
    if (!graph.nodes[run.current]) {
      error = 'The step this run was on has been removed. Start the run again.'
      return
    }
    busy = true
    try {
      const res = await apiFetch('/api/decisions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          provider: picked.provider, model: picked.model, state: apiState,
          graph, from: run.current, max_steps: maxSteps,
        }),
      })
      const text = await res.text()
      let body = null
      try { body = JSON.parse(text) } catch {}
      if (body?.trace?.steps?.length) run = applyTrace(run, body.trace.steps)
      if (!res.ok) error = body?.error ?? (text.trim() || `Request failed (${res.status})`)
    } catch (_) {
      error = 'Could not reach the decision model.'
    } finally {
      busy = false
    }
  }

  function takeBranch(outcome) {
    if (run && active) run = overrideLast(run, $state.snapshot(active), outcome)
  }

  function questionFor(nodeId) {
    try { return toApiGraph($state.snapshot(active)).nodes[nodeId]?.question } catch { return null }
  }

  // ── Map ────────────────────────────────────────────────────────────────────
  const BOX_W = 168, BOX_H = 40, COL_GAP = 64, ROW_GAP = 22, PAD = 8

  let map = $derived.by(() => {
    if (!active) return null
    const { cols, unreachable } = layout(active)
    const pos = {}
    cols.forEach((col, c) => col.forEach((id, r) => {
      pos[id] = { x: PAD + c * (BOX_W + COL_GAP), y: PAD + r * (BOX_H + ROW_GAP) }
    }))
    const visited = new Set(run?.steps.map(s => s.node) ?? [])
    const taken = new Set((run?.steps ?? []).filter(s => s.next).map(s => `${s.node}>${stepOutcome(s)}`))
    const edges = []
    for (const n of active.nodes) {
      const labels = Object.fromEntries(rowOutcomes(n.row).map(o => [o.key, o.label]))
      for (const [outcome, to] of Object.entries(n.edges)) {
        if (!pos[n.id] || !pos[to] || !(outcome in labels)) continue
        const a = pos[n.id], b = pos[to]
        const x1 = a.x + BOX_W, y1 = a.y + BOX_H / 2, x2 = b.x, y2 = b.y + BOX_H / 2
        const mx = (x1 + x2) / 2
        edges.push({
          id: `${n.id}>${outcome}`, label: labels[outcome],
          d: `M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`,
          lx: mx, ly: (y1 + y2) / 2 - 4, taken: taken.has(`${n.id}>${outcome}`),
        })
      }
    }
    const width = PAD * 2 + cols.length * BOX_W + Math.max(0, cols.length - 1) * COL_GAP
    const height = PAD * 2 + Math.max(1, ...cols.map(c => c.length)) * (BOX_H + ROW_GAP) - ROW_GAP
    return { pos, edges, width, height, visited, unreachable: new Set(unreachable) }
  })

  const short = (t, n = 24) => (t.length > n ? t.slice(0, n - 1) + '…' : t)

  function stopText(step) {
    if (step.next) return null
    if (step.stopped === 'low_confidence') return 'Paused: the model was unsure. Pick a branch below.'
    return 'Ends here.'
  }
</script>

<PageLayout title="Decision graphs" {onBack}
  hint="Chain questions for a decision model: each answer picks the next step. Graphs are saved in this browser."
  actionLabel="New graph" onAction={newGraph}>

  {#if active}
    <div class="toolbar">
      <select value={activeId} onchange={e => selectGraph(e.currentTarget.value)} title="Saved graphs">
        {#each graphs as g (g.id)}<option value={g.id}>{g.name || 'Untitled graph'}</option>{/each}
      </select>
      <input class="name" bind:value={active.name} placeholder="Graph name" />
      <button class="ghost danger" onclick={deleteGraph}>Delete graph</button>
    </div>

    <div class="layout">
      <!-- ── Steps editor ─────────────────────────────────────────────── -->
      <section class="steps">
        <h3>Steps</h3>
        {#each active.nodes as node, i (node.id)}
          {@const outcomes = rowOutcomes(node.row)}
          <div class="node" class:root={node.id === active.root}
               class:current={run?.current === node.id}
               class:unreachable={map?.unreachable.has(node.id)}>
            <div class="node-head">
              <span class="node-num">Step {i + 1}</span>
              {#if node.id === active.root}
                <span class="badge">start</span>
              {:else}
                <button class="ghost" onclick={() => active.root = node.id}>Start here</button>
              {/if}
              {#if map?.unreachable.has(node.id)}<span class="badge muted" title="No branch leads here from the start">not reached</span>{/if}
              {#if active.nodes.length > 1}
                <button class="ghost icon" onclick={() => deleteNode(node.id)} title="Remove step">✕</button>
              {/if}
            </div>
            <div class="q-row">
              <select bind:value={node.row.type} title="Question type">
                {#each QUESTION_TYPES as t}<option value={t.value}>{t.label}</option>{/each}
              </select>
              <input class="q-text" bind:value={node.row.instructions} placeholder="Question, e.g. &quot;Is this request urgent?&quot;" />
            </div>
            {#if node.row.type === 'choice'}
              <input class="q-extra" bind:value={node.row.options} placeholder="Options: billing, technical, sales" title="Comma separated, or one per line as name: description" />
            {:else if node.row.type === 'score'}
              <input class="q-extra" bind:value={node.row.levels} placeholder="Levels, lowest first: low, medium, high" />
            {/if}
            {#if outcomes.length}
              <div class="edges">
                {#each outcomes as o (o.key)}
                  <label class="edge">
                    <span class="edge-label">If <b>{o.label}</b></span>
                    <select value={node.edges[o.key] ?? ''} onchange={e => setEdge(node, o.key, e.currentTarget.value)}>
                      <option value="">end here</option>
                      {#each active.nodes as other, j (other.id)}
                        {#if other.id !== node.id}<option value={other.id}>go to Step {j + 1}: {short(nodeTitle(active, other.id), 32)}</option>{/if}
                      {/each}
                      {#if active.nodes.length < MAX_NODES}<option value="__new">go to a new step</option>{/if}
                    </select>
                  </label>
                {/each}
              </div>
            {/if}
            <label class="floor" title="If the model's confidence is below this, the run pauses here and you pick the branch">
              Pause for me below
              <input type="number" min="0" max="100" step="5" bind:value={node.minConfidence} placeholder="–" />
              % confidence
            </label>
          </div>
        {/each}
        <button class="add" onclick={addNode} disabled={active.nodes.length >= MAX_NODES}>+ Add step</button>
      </section>

      <!-- ── Map and run ──────────────────────────────────────────────── -->
      <section class="run">
        <h3>Map</h3>
        {#if map}
          <div class="map">
            <svg width={map.width} height={map.height} viewBox="0 0 {map.width} {map.height}" role="img" aria-label="Decision graph map">
              {#each map.edges as e (e.id)}
                <path d={e.d} class="edge-line" class:taken={e.taken} />
                <text x={e.lx} y={e.ly} class="edge-text" class:taken={e.taken} text-anchor="middle">{short(e.label, 14)}</text>
              {/each}
              {#each active.nodes as n, i (n.id)}
                {#if map.pos[n.id]}
                  {@const p = map.pos[n.id]}
                  <g class="box" class:visited={map.visited.has(n.id)} class:current={run?.current === n.id}
                     class:unreachable={map.unreachable.has(n.id)}>
                    <rect x={p.x} y={p.y} width={BOX_W} height={BOX_H} rx="7" />
                    <text x={p.x + 10} y={p.y + 16} class="box-num">Step {i + 1}{n.id === active.root ? ' · start' : ''}</text>
                    <text x={p.x + 10} y={p.y + 31} class="box-text">{short(nodeTitle(active, n.id))}</text>
                  </g>
                {/if}
              {/each}
            </svg>
          </div>
        {/if}

        <h3>Run</h3>
        {#if modelsLoaded && !models.length}
          <div class="notice">No decision model is set up yet. Add one to a provider's <code>model_capabilities</code> with <code>decision</code> in the config to run graphs.</div>
        {:else}
          <div class="run-controls">
            <select bind:value={modelKey} title="Decision model">
              {#each models as m (key(m))}<option value={key(m)}>{m.provider} / {m.model}</option>{/each}
            </select>
          </div>
        {/if}
        <textarea bind:value={stateText} rows="3" placeholder="Situation to decide on: text, or JSON." disabled={busy}></textarea>
        <div class="run-controls">
          <button class="primary" onclick={() => advance(1)} disabled={busy || !picked || !stateText.trim() || paused}>
            {busy ? 'Asking…' : (run?.current && run.steps.length ? 'Next step' : run?.done ? 'Step from the start' : 'Step')}
          </button>
          <button class="secondary" onclick={() => advance(0)} disabled={busy || !picked || !stateText.trim() || paused}>
            {run?.current && run.steps.length ? 'Run to the end' : run?.done ? 'Run again' : 'Run to the end'}
          </button>
          {#if run?.steps.length}<button class="ghost" onclick={resetRun} disabled={busy}>Reset</button>{/if}
        </div>
        {#if error}<div class="d-error">{error}</div>{/if}

        {#if run?.steps.length}
          <ol class="path">
            {#each run.steps as step, i (i)}
              {@const node = active.nodes.find(n => n.id === step.node)}
              {@const last = i === run.steps.length - 1}
              <li class="path-step">
                <div class="ps-head">
                  <span class="ps-title">{nodeTitle(active, step.node)}</span>
                  {#if step.by === 'you'}<span class="badge">your pick</span>{/if}
                </div>
                <div class="ps-answer">
                  Model: {summarize(step.answer)}
                  {#if step.answer.confidence != null}<span class="ps-conf">confidence {pct(step.answer.confidence)}</span>{/if}
                  {#if step.by === 'you'}<span class="ps-conf">you took {rowOutcomes(node?.row).find(o => o.key === step.chosen)?.label ?? step.chosen}</span>{/if}
                </div>
                {#if step.next}
                  <div class="ps-next">→ {nodeTitle(active, step.next)}</div>
                {:else}
                  <div class="ps-next end">{stopText(step)}</div>
                {/if}
                {#if last && node && !busy}
                  {@const outs = rowOutcomes(node.row)}
                  {#if outs.length}
                    <div class="branch">
                      <span>{step.stopped === 'low_confidence' ? 'Go with' : 'Take a different branch:'}</span>
                      {#each outs as o (o.key)}
                        <button class="chip" class:on={stepOutcome(step) === o.key} onclick={() => takeBranch(o.key)}>{o.label}</button>
                      {/each}
                    </div>
                  {/if}
                {/if}
                <details>
                  <summary>Probabilities</summary>
                  <DecisionResult entry={{ model: picked?.model ?? '', questions: [questionFor(step.node)].filter(Boolean), result: { answers: [step.answer] } }} />
                </details>
              </li>
            {/each}
          </ol>
          {#if run.done && run.steps[run.steps.length - 1]?.stopped !== 'low_confidence'}
            <div class="done">Run finished after {run.steps.length} step{run.steps.length === 1 ? '' : 's'}.</div>
          {/if}
        {/if}
      </section>
    </div>
  {/if}
</PageLayout>

<style>
  .toolbar { display: flex; gap: 0.5rem; align-items: center; margin-bottom: 1rem; flex-wrap: wrap; }
  .toolbar .name { flex: 1; min-width: 10rem; max-width: 24rem; }

  .layout { display: flex; gap: 1.5rem; align-items: flex-start; flex-wrap: wrap; }
  .steps { flex: 1 1 380px; min-width: 0; display: flex; flex-direction: column; gap: 0.6rem; }
  .run   { flex: 1 1 420px; min-width: 0; display: flex; flex-direction: column; gap: 0.6rem; }
  h3 { font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.08em; color: var(--text3); font-weight: 600; margin-top: 0.25rem; }

  select, input, textarea {
    background: var(--bg); border: 1px solid var(--border); border-radius: 6px;
    color: var(--text); font: inherit; font-size: 0.82rem; padding: 0.32rem 0.5rem; outline: none;
  }
  textarea { font-size: 0.9rem; resize: vertical; line-height: 1.45; }

  .node {
    background: var(--surface); border: 1px solid var(--border); border-radius: 10px;
    padding: 0.6rem 0.75rem; display: flex; flex-direction: column; gap: 0.4rem;
  }
  .node.root { border-color: var(--accent-dark); }
  .node.current { box-shadow: 0 0 0 1px var(--accent); }
  .node.unreachable { opacity: 0.75; }
  .node-head { display: flex; gap: 0.5rem; align-items: center; }
  .node-num { font-size: 0.78rem; font-weight: 600; color: var(--text2); }
  .node-head .icon { margin-left: auto; }
  .q-row { display: flex; gap: 0.4rem; }
  .q-text { flex: 1; min-width: 0; }
  .q-extra { width: 100%; }
  .edges { display: flex; flex-direction: column; gap: 0.25rem; padding-left: 0.25rem; }
  .edge { display: flex; gap: 0.5rem; align-items: center; font-size: 0.8rem; color: var(--text3); }
  .edge-label { min-width: 7rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .edge-label b { color: var(--text2); font-weight: 500; }
  .edge select { flex: 1; min-width: 0; }
  .floor { font-size: 0.75rem; color: var(--text4); display: flex; gap: 0.35rem; align-items: center; }
  .floor input { width: 4.2rem; padding: 0.15rem 0.35rem; }

  .badge {
    font-size: 0.65rem; text-transform: uppercase; letter-spacing: 0.06em; font-weight: 700;
    color: var(--accent); border: 1px solid var(--accent-dark); border-radius: 4px; padding: 0 0.3rem;
  }
  .badge.muted { color: var(--text4); border-color: var(--border); }

  button { font: inherit; cursor: pointer; }
  button:disabled { opacity: 0.4; cursor: not-allowed; }
  .ghost { background: none; border: none; color: var(--text4); font-size: 0.78rem; padding: 0.1rem 0.2rem; }
  .ghost:hover:not(:disabled) { color: var(--text2); }
  .ghost.danger:hover { color: var(--danger); }
  .add { align-self: flex-start; background: none; border: none; color: var(--text3); font-size: 0.82rem; }
  .add:hover:not(:disabled) { color: var(--text); }
  .primary, .secondary { border-radius: 7px; padding: 0.4rem 0.95rem; font-size: 0.85rem; }
  .primary { background: var(--accent); color: var(--bg); border: none; font-weight: 500; }
  .primary:hover:not(:disabled) { background: var(--accent-light); }
  .secondary { background: var(--surface-hi); color: var(--text); border: 1px solid var(--border); }
  .secondary:hover:not(:disabled) { background: var(--hover); }
  .run-controls { display: flex; gap: 0.5rem; align-items: center; flex-wrap: wrap; }

  .map {
    background: var(--depth); border: 1px solid var(--border); border-radius: 10px;
    overflow: auto; max-height: 360px; padding: 0.25rem;
  }
  .map svg { display: block; }
  .edge-line { fill: none; stroke: var(--border); stroke-width: 1.5; }
  .edge-line.taken { stroke: var(--accent); stroke-width: 2.25; }
  .edge-text { font-size: 10px; fill: var(--text4); }
  .edge-text.taken { fill: var(--accent-light); }
  .box rect { fill: var(--surface); stroke: var(--border); stroke-width: 1; }
  .box.visited rect { stroke: var(--accent); stroke-width: 1.75; }
  .box.current rect { stroke: var(--accent-light); stroke-dasharray: 4 3; stroke-width: 1.75; }
  .box.unreachable rect { stroke-dasharray: 2 3; }
  .box-num { font-size: 9.5px; fill: var(--text4); text-transform: uppercase; letter-spacing: 0.05em; }
  .box-text { font-size: 11.5px; fill: var(--text); }

  .notice { font-size: 0.82rem; color: var(--text3); background: var(--surface); border: 1px dashed var(--border); border-radius: 8px; padding: 0.6rem 0.75rem; }
  .notice code { font-size: 0.78rem; color: var(--text2); }
  .d-error { color: var(--danger-light); font-size: 0.82rem; }

  .path { list-style: none; display: flex; flex-direction: column; gap: 0.5rem; counter-reset: step; }
  .path-step {
    counter-increment: step; position: relative; padding: 0.55rem 0.75rem 0.55rem 2.1rem;
    background: var(--surface); border: 1px solid var(--border); border-radius: 10px;
    display: flex; flex-direction: column; gap: 0.25rem;
  }
  .path-step::before {
    content: counter(step); position: absolute; left: 0.65rem; top: 0.55rem;
    font-size: 0.72rem; font-weight: 700; color: var(--accent);
  }
  .ps-head { display: flex; gap: 0.5rem; align-items: center; }
  .ps-title { font-size: 0.85rem; color: var(--text2); }
  .ps-answer { font-size: 0.9rem; color: var(--text); font-weight: 500; display: flex; gap: 0.6rem; align-items: baseline; flex-wrap: wrap; }
  .ps-conf { font-size: 0.72rem; color: var(--text4); font-weight: 400; }
  .ps-next { font-size: 0.8rem; color: var(--accent-light); }
  .ps-next.end { color: var(--text3); }
  .branch { display: flex; gap: 0.35rem; align-items: center; flex-wrap: wrap; font-size: 0.78rem; color: var(--text3); }
  .chip {
    background: var(--bg); border: 1px solid var(--border); color: var(--text2);
    border-radius: 999px; padding: 0.1rem 0.6rem; font-size: 0.78rem;
  }
  .chip:hover { border-color: var(--accent-dark); color: var(--text); }
  .chip.on { border-color: var(--accent); color: var(--accent-light); }
  details summary { font-size: 0.72rem; color: var(--text4); cursor: pointer; }
  details :global(.decision) { margin-top: 0.4rem; max-width: none; }
  .done { font-size: 0.8rem; color: var(--text3); }
</style>
