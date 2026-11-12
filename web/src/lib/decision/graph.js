// Decision graphs: build one in the editor, turn it into the API's graph,
// lay it out for drawing, and track a run through it. Pure functions, tested
// with `node --test`.
//
// Each node asks a decision model (e.g. Liquid d1) one question; the answer's
// outcome picks the edge to the next node, and a node with no edge for an
// outcome ends the run there. The server walks the graph (POST /decisions
// with graph, from, max_steps), so the browser only keeps the run's path.
//
// Editor shape:
//   { id, name, root, nodes: [{ id, row, minConfidence, edges }], updatedAt }
// row is a decision/form.js question row; minConfidence is a percent ('' for
// none); edges maps outcome key → node id.

import { blankQuestion, rowOutcomes, toApiQuestions } from './form.js'

export const MAX_NODES = 64 // matches decision.MaxGraphNodes on the server

export const STORAGE_KEY = 'stoa-decision-graphs'

function nextNodeId(graph) {
  let n = graph.nodes.length + 1
  const used = new Set(graph.nodes.map(x => x.id))
  while (used.has(`n${n}`)) n++
  return `n${n}`
}

export function blankNode(graph) {
  const id = nextNodeId(graph)
  return { id, row: blankQuestion(Number(id.slice(1))), minConfidence: '', edges: {} }
}

export function blankGraph(name = 'Untitled graph') {
  const g = { id: `g-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`, name, root: '', nodes: [], updatedAt: Date.now() }
  const first = blankNode(g)
  g.nodes.push(first)
  g.root = first.id
  return g
}

// A small worked example, so the screen isn't empty the first time.
export function exampleGraph() {
  const g = blankGraph('Support ticket triage')
  const [urgent] = g.nodes
  urgent.row = { ...urgent.row, type: 'noul', instructions: 'Does this ticket need a reply today?' }
  const team = { id: 'n2', row: { ...blankQuestion(2), type: 'choice', instructions: 'Which team should handle it?', options: 'billing: Payments, invoices and refunds\ntechnical: Bugs, outages and errors\nsales: Plans and upgrades' }, minConfidence: '60', edges: {} }
  const severity = { id: 'n3', row: { ...blankQuestion(3), type: 'score', instructions: 'How badly is the customer affected?', levels: 'barely, some features, cannot work' }, minConfidence: '', edges: {} }
  urgent.edges = { true: 'n2' }
  team.edges = { technical: 'n3' }
  g.nodes.push(team, severity)
  return g
}

export function nodeTitle(graph, id) {
  const i = graph.nodes.findIndex(n => n.id === id)
  if (i < 0) return id
  const text = graph.nodes[i].row.instructions?.trim()
  return text || `Step ${i + 1}`
}

// Remove a node and every edge into it. The root moves to the first node left.
export function removeNode(graph, id) {
  const nodes = graph.nodes.filter(n => n.id !== id).map(n => {
    const edges = Object.fromEntries(Object.entries(n.edges).filter(([, to]) => to !== id))
    return { ...n, edges }
  })
  const root = graph.root === id ? (nodes[0]?.id ?? '') : graph.root
  return { ...graph, nodes, root }
}

// Edges that still point at a real outcome and a real node.
export function liveEdges(graph, node) {
  const ids = new Set(graph.nodes.map(n => n.id))
  const keys = new Set(rowOutcomes(node.row).map(o => o.key))
  return Object.fromEntries(Object.entries(node.edges ?? {}).filter(([k, to]) => to && keys.has(k) && ids.has(to)))
}

// The first node found on a cycle, or null.
export function findCycle(graph) {
  const byId = new Map(graph.nodes.map(n => [n.id, n]))
  const colour = new Map()
  const visit = id => {
    colour.set(id, 1)
    for (const to of Object.values(liveEdges(graph, byId.get(id)))) {
      if (colour.get(to) === 1) return to
      if (!colour.get(to)) {
        const c = visit(to)
        if (c) return c
      }
    }
    colour.set(id, 2)
    return null
  }
  for (const n of graph.nodes) {
    if (!colour.get(n.id)) {
      const c = visit(n.id)
      if (c) return c
    }
  }
  return null
}

// Turn the editor's graph into the API's { root, nodes }. Throws an Error
// with a message fit to show when something is missing.
export function toApiGraph(graph) {
  if (!graph.nodes.length) throw new Error('Add at least one step.')
  if (graph.nodes.length > MAX_NODES) throw new Error(`A graph can have at most ${MAX_NODES} steps.`)
  if (!graph.nodes.some(n => n.id === graph.root)) throw new Error('Pick a starting step.')
  const cyc = findCycle(graph)
  if (cyc) throw new Error(`The graph loops back to "${nodeTitle(graph, cyc)}". Graphs can't have loops yet.`)
  const nodes = {}
  graph.nodes.forEach((n, i) => {
    let q
    try {
      [q] = toApiQuestions([n.row])
    } catch (e) {
      throw new Error(e.message.replace(/^Question 1\b/, `Step ${i + 1}`))
    }
    const node = { question: { ...q, name: n.id } }
    const edges = liveEdges(graph, n)
    if (Object.keys(edges).length) node.edges = edges
    const mc = String(n.minConfidence ?? '').trim()
    if (mc !== '') {
      const v = Number(mc)
      if (!Number.isFinite(v) || v < 0 || v > 100) throw new Error(`Step ${i + 1}: the confidence floor must be 0 to 100.`)
      node.min_confidence = v / 100
    }
    nodes[n.id] = node
  })
  return { root: graph.root, nodes }
}

// Columns for drawing: each node goes in the column of its longest path from
// the root, so edges always point right. Nodes the root can't reach go in a
// last column and are listed in `unreachable`. Assumes no cycles.
export function layout(graph) {
  const byId = new Map(graph.nodes.map(n => [n.id, n]))
  const depth = new Map()
  if (byId.has(graph.root)) {
    depth.set(graph.root, 0)
    // Longest-path relaxation; at most |nodes| rounds on a DAG.
    for (let round = 0; round < graph.nodes.length; round++) {
      let changed = false
      for (const [id, d] of [...depth]) {
        for (const to of Object.values(liveEdges(graph, byId.get(id)))) {
          if ((depth.get(to) ?? -1) < d + 1) { depth.set(to, d + 1); changed = true }
        }
      }
      if (!changed) break
    }
  }
  const cols = []
  const unreachable = []
  for (const n of graph.nodes) {
    const d = depth.get(n.id)
    if (d == null) { unreachable.push(n.id); continue }
    ;(cols[d] ??= []).push(n.id)
  }
  if (unreachable.length) cols.push(unreachable)
  return { cols: cols.map(c => c ?? []), unreachable }
}

// ── Runs ───────────────────────────────────────────────────────────────────
//
// A run is { current, steps, done }. current is the node to ask next (null
// when the run is over); done is null, 'leaf' or 'low_confidence'. Each step
// is the server's StepResult plus `by`: 'model', or 'you' when a person
// picked the branch.

export function newRun(graph) {
  return { current: graph.root || null, steps: [], done: null }
}

// Add the steps a POST /decisions graph call returned.
export function applyTrace(run, steps) {
  if (!steps?.length) return run
  const added = steps.map(s => ({ ...s, by: 'model' }))
  const last = added[added.length - 1]
  return {
    current: last.next || null,
    steps: [...run.steps, ...added],
    done: last.next ? null : (last.stopped || 'leaf'),
  }
}

// A person takes a different branch at the last step. outcome is an outcome
// key of that step's node.
export function overrideLast(run, graph, outcome) {
  if (!run.steps.length) return run
  const last = run.steps[run.steps.length - 1]
  const node = graph.nodes.find(n => n.id === last.node)
  if (!node) return run
  const next = liveEdges(graph, node)[outcome] ?? ''
  const step = { ...last, chosen: outcome, next, stopped: next ? '' : 'leaf', by: 'you' }
  return {
    current: next || null,
    steps: [...run.steps.slice(0, -1), step],
    done: next ? null : 'leaf',
  }
}

// The outcome a step went with: the person's choice, else the model's pick.
export function stepOutcome(step) {
  return step.chosen ?? step.answer?.pick ?? ''
}

// ── Saving ─────────────────────────────────────────────────────────────────
// Graphs live in the browser for now (no server storage yet).

export function loadGraphs(storage) {
  try {
    const raw = storage?.getItem(STORAGE_KEY)
    const list = raw ? JSON.parse(raw) : []
    return Array.isArray(list) ? list.filter(g => g && Array.isArray(g.nodes)) : []
  } catch {
    return []
  }
}

export function saveGraphs(storage, graphs) {
  try {
    storage?.setItem(STORAGE_KEY, JSON.stringify(graphs))
    return true
  } catch {
    return false
  }
}
