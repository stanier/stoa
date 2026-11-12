// Where decision graphs are kept. The server (agentd /decision-graphs, per
// user) is the normal home; the browser's localStorage is the fallback when
// agentd runs without a database, and the place graphs lived before the
// server kept them. Both backends have the same shape:
//
//   list()        → [{ id, name, updatedAt }]  most recent first
//   get(id)       → editor graph { id, name, root, nodes, updatedAt }
//   create(graph) → the stored graph, with its new id
//   save(graph)   → the stored graph
//   remove(id)
//
// Functions take fetch and storage as arguments so `node --test` can run them.

import { STORAGE_KEY, loadGraphs, saveGraphs } from './graph.js'

export class StoreError extends Error {
  constructor(message, status) {
    super(message)
    this.status = status
  }
}

// The document the server stores: everything but the id and name.
export function toServerBody(graph) {
  return { name: (graph.name ?? '').trim() || 'Untitled graph', graph: { root: graph.root, nodes: graph.nodes } }
}

export function fromServer(wire) {
  const doc = wire.graph ?? {}
  return {
    id: wire.id,
    name: wire.name,
    root: doc.root ?? '',
    nodes: Array.isArray(doc.nodes) ? doc.nodes : [],
    updatedAt: Date.parse(wire.updated_at) || Date.now(),
  }
}

export function serverStore(fetchFn, base = '/api/decision-graphs') {
  async function call(path, init = {}) {
    let res
    try {
      res = await fetchFn(base + path, {
        ...init,
        headers: init.body ? { 'Content-Type': 'application/json' } : undefined,
      })
    } catch {
      throw new StoreError('Could not reach the server.', 0)
    }
    if (res.status === 204) return null
    const text = await res.text()
    if (!res.ok) throw new StoreError(text.trim() || `Request failed (${res.status})`, res.status)
    return text ? JSON.parse(text) : null
  }
  return {
    kind: 'server',
    async list() {
      const rows = (await call('')) ?? []
      return rows.map(r => ({ id: r.id, name: r.name, updatedAt: Date.parse(r.updated_at) || 0 }))
    },
    async get(id) { return fromServer(await call(`/${encodeURIComponent(id)}`)) },
    async create(graph) {
      return fromServer(await call('', { method: 'POST', body: JSON.stringify(toServerBody(graph)) }))
    },
    async save(graph) {
      const out = await call(`/${encodeURIComponent(graph.id)}`, { method: 'PUT', body: JSON.stringify(toServerBody(graph)) })
      return out ? fromServer(out) : graph
    },
    async remove(id) { await call(`/${encodeURIComponent(id)}`, { method: 'DELETE' }) },
  }
}

export function localStore(storage) {
  const all = () => loadGraphs(storage)
  const write = list => {
    if (!saveGraphs(storage, list)) throw new StoreError('Could not save in this browser.', 0)
  }
  return {
    kind: 'local',
    async list() {
      return all().map(g => ({ id: g.id, name: g.name, updatedAt: g.updatedAt ?? 0 })).sort((a, b) => b.updatedAt - a.updatedAt)
    },
    async get(id) {
      const g = all().find(x => x.id === id)
      if (!g) throw new StoreError('Graph not found.', 404)
      return g
    },
    async create(graph) {
      const g = { ...graph, updatedAt: Date.now() }
      write([...all().filter(x => x.id !== g.id), g])
      return g
    },
    async save(graph) {
      const g = { ...graph, updatedAt: Date.now() }
      write(all().map(x => (x.id === g.id ? g : x)))
      return g
    },
    async remove(id) { write(all().filter(x => x.id !== id)) },
  }
}

// Copy graphs saved in this browser to the server, then forget the ones that
// made it. Returns how many moved and how many are still only local.
export async function moveLocalToServer(storage, server) {
  const local = loadGraphs(storage)
  const left = []
  let moved = 0
  for (const g of local) {
    try {
      await server.create(g)
      moved++
    } catch {
      left.push(g)
    }
  }
  if (local.length) {
    if (left.length) saveGraphs(storage, left)
    else {
      try { storage?.removeItem(STORAGE_KEY) } catch { /* storage blocked */ }
    }
  }
  return { moved, left: left.length }
}
