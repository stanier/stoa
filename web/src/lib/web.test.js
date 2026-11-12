// Unit tests for the web UI's pure helpers. Run with `npm test` (node --test).
import { test } from 'node:test'
import assert from 'node:assert/strict'

import { resample, floatToInt16, int16ToFloat, int16ToBase64, base64ToInt16, rms, EnergyVAD } from './voice/pcm.js'
import { toApiQuestions, toApiState, summarize, slug } from './decision/form.js'

test('resample keeps duration and endpoints', () => {
  const input = Float32Array.from({ length: 480 }, (_, i) => i / 479)
  const out = resample(input, 48000, 16000)
  assert.equal(out.length, 160)
  assert.equal(out[0], 0)
  assert.ok(Math.abs(out[159] - 477 / 479) < 0.01)
  assert.equal(resample(input, 16000, 16000).length, 480)
})

test('PCM16 round trip through base64 is little-endian and lossless', () => {
  const pcm = Int16Array.from([0, 1, -1, 32767, -32768, 258])
  const b64 = int16ToBase64(pcm)
  assert.equal(Buffer.from(b64, 'base64')[10], 0x02) // 258 = 0x0102, low byte first
  assert.deepEqual(Array.from(base64ToInt16(b64)), Array.from(pcm))
})

test('float/int16 conversion clamps', () => {
  assert.deepEqual(Array.from(floatToInt16(Float32Array.from([2, -2, 0]))), [32767, -32768, 0])
  assert.equal(int16ToFloat(Int16Array.from([-32768]))[0], -1)
})

test('rms of silence and a full-scale square', () => {
  assert.equal(rms(new Float32Array(10)), 0)
  assert.equal(rms(Float32Array.from([1, -1, 1, -1])), 1)
})

test('EnergyVAD needs attack time to start and a pause to end', () => {
  const vad = new EnergyVAD({ threshold: 0.1, attackMs: 100, hangMs: 500 })
  assert.equal(vad.push(0.5, 0), null)
  assert.equal(vad.push(0.5, 50), null)
  assert.equal(vad.push(0.5, 100), 'start')
  assert.equal(vad.push(0.0, 200), null)
  assert.equal(vad.push(0.5, 300), null) // a short dip doesn't end the turn
  assert.equal(vad.push(0.0, 400), null)
  assert.equal(vad.push(0.0, 899), null)
  assert.equal(vad.push(0.0, 900), 'end')
  assert.equal(vad.speaking, false)
})

test('toApiQuestions builds noul, choice and score questions', () => {
  const qs = toApiQuestions([
    { name: '', type: 'noul', instructions: 'Is this urgent?' },
    { name: 'team', type: 'choice', instructions: 'Which team?', options: 'billing: Payments and refunds\ntechnical\nsales: Plans' },
    { name: 'team', type: 'score', instructions: 'How bad?', levels: 'none, minor, major' },
  ])
  assert.deepEqual(qs[0], { name: 'is_this_urgent', type: 'noul', instructions: 'Is this urgent?' })
  assert.deepEqual(qs[1].criteria, { billing: 'Payments and refunds', technical: null, sales: 'Plans' })
  assert.equal(qs[2].name, 'team_2') // names stay unique
  assert.deepEqual(qs[2].criteria, ['none', 'minor', 'major'])
})

test('toApiQuestions explains what is missing', () => {
  assert.throws(() => toApiQuestions([]), /at least one/)
  assert.throws(() => toApiQuestions([{ type: 'noul', instructions: '' }]), /question text/)
  assert.throws(() => toApiQuestions([{ type: 'choice', instructions: 'x', options: 'one' }]), /two options/)
  assert.throws(() => toApiQuestions([{ type: 'score', instructions: 'x', levels: 'a' }]), /2 to 10/)
})

test('toApiState sends JSON when it parses, text otherwise', () => {
  assert.deepEqual(toApiState('{"a": 1}'), { a: 1 })
  assert.equal(toApiState('  my app crashed '), 'my app crashed')
  assert.equal(toApiState('{not json'), '{not json')
  assert.throws(() => toApiState(' '), /situation/)
})

test('summarize answers', () => {
  assert.equal(summarize({ type: 'noul', pick: 'true', value: 0.981 }), 'yes (98% yes)')
  assert.equal(summarize({ type: 'choice', pick: 'technical', options: [{ key: 'technical', label: 'technical', probability: 0.973 }] }), 'technical (97%)')
  assert.equal(summarize({ type: 'score', pick: '3', value: 2.582, options: [{ key: '3', label: 'Critical', probability: 0.6 }] }), 'Critical · 2.58')
  assert.equal(summarize({ type: 'box' }), 'box answer')
  assert.equal(slug('  Hello, World! '), 'hello_world')
})

// ── Decision graphs ──────────────────────────────────────────────────────────
import { rowOutcomes } from './decision/form.js'
import {
  exampleGraph, blankGraph, blankNode, removeNode, findCycle, toApiGraph, layout,
  newRun, applyTrace, overrideLast, stepOutcome, loadGraphs, saveGraphs, STORAGE_KEY,
} from './decision/graph.js'

test('rowOutcomes uses the server outcome keys', () => {
  assert.deepEqual(rowOutcomes({ type: 'noul' }).map(o => o.key), ['true', 'false'])
  assert.deepEqual(rowOutcomes({ type: 'choice', options: 'a: first, b, a' }).map(o => o.key), ['a', 'b'])
  assert.deepEqual(rowOutcomes({ type: 'score', levels: 'low\nhigh' }), [{ key: '0', label: 'low' }, { key: '1', label: 'high' }])
  assert.deepEqual(rowOutcomes({ type: 'choice', options: '' }), [])
})

test('toApiGraph names questions by node and keeps only live edges', () => {
  const g = exampleGraph()
  g.nodes[0].edges.false = 'gone'            // missing node
  g.nodes[1].edges.marketing = 'n3'          // not an option
  const api = toApiGraph(g)
  assert.equal(api.root, 'n1')
  assert.deepEqual(Object.keys(api.nodes), ['n1', 'n2', 'n3'])
  assert.equal(api.nodes.n1.question.name, 'n1')
  assert.deepEqual(api.nodes.n1.edges, { true: 'n2' })
  assert.deepEqual(api.nodes.n2.edges, { technical: 'n3' })
  assert.equal(api.nodes.n2.min_confidence, 0.6)
  assert.equal(api.nodes.n3.edges, undefined)
  assert.deepEqual(api.nodes.n3.question.criteria, ['barely', 'some features', 'cannot work'])
})

test('toApiGraph explains what is wrong', () => {
  const g = blankGraph()
  assert.throws(() => toApiGraph(g), /Step 1 needs its question text/)
  const loop = exampleGraph()
  loop.nodes[2].edges = { 0: 'n1' }
  assert.equal(findCycle(loop), 'n1')
  assert.throws(() => toApiGraph(loop), /loops back/)
  const bad = exampleGraph()
  bad.nodes[0].minConfidence = '150'
  assert.throws(() => toApiGraph(bad), /0 to 100/)
  assert.throws(() => toApiGraph({ ...exampleGraph(), root: 'x' }), /starting step/)
})

test('removeNode drops edges into it and moves the root', () => {
  const g = removeNode(exampleGraph(), 'n1')
  assert.equal(g.root, 'n2')
  const h = removeNode(exampleGraph(), 'n2')
  assert.deepEqual(h.nodes[0].edges, {})
  const added = blankNode(h)
  assert.ok(!h.nodes.some(n => n.id === added.id))
})

test('layout puts each node in its longest-path column', () => {
  const g = exampleGraph()
  g.nodes[0].edges.false = 'n3'  // n3 reachable at depth 1 and 2
  g.nodes.push({ ...blankNode(g), id: 'n9' })
  const { cols, unreachable } = layout(g)
  assert.deepEqual(cols, [['n1'], ['n2'], ['n3'], ['n9']])
  assert.deepEqual(unreachable, ['n9'])
})

test('a run follows steps, pauses, and takes a person\'s branch', () => {
  const g = exampleGraph()
  let run = newRun(g)
  assert.equal(run.current, 'n1')
  run = applyTrace(run, [{ node: 'n1', answer: { pick: 'true' }, next: 'n2' }])
  assert.equal(run.current, 'n2')
  assert.equal(run.done, null)
  run = applyTrace(run, [{ node: 'n2', answer: { pick: 'billing' }, stopped: 'low_confidence' }])
  assert.equal(run.current, null)
  assert.equal(run.done, 'low_confidence')
  run = overrideLast(run, g, 'technical')
  assert.equal(run.current, 'n3')
  assert.equal(run.steps[1].by, 'you')
  assert.equal(stepOutcome(run.steps[1]), 'technical')
  run = overrideLast(run, g, 'sales')  // no edge: the run ends there
  assert.equal(run.current, null)
  assert.equal(run.done, 'leaf')
})

test('graphs save to and load from storage, and bad data is ignored', () => {
  const mem = new Map()
  const storage = { getItem: k => mem.get(k) ?? null, setItem: (k, v) => mem.set(k, v) }
  assert.deepEqual(loadGraphs(storage), [])
  const g = exampleGraph()
  assert.ok(saveGraphs(storage, [g]))
  assert.equal(loadGraphs(storage)[0].name, 'Support ticket triage')
  mem.set(STORAGE_KEY, '{nope')
  assert.deepEqual(loadGraphs(storage), [])
  assert.equal(saveGraphs({ setItem() { throw new Error('full') } }, [g]), false)
})

// ── Where graphs are saved ───────────────────────────────────────────────────
import { serverStore, localStore, moveLocalToServer, toServerBody, fromServer } from './decision/store.js'

function memStorage(init = {}) {
  const m = new Map(Object.entries(init))
  return { getItem: k => m.get(k) ?? null, setItem: (k, v) => m.set(k, v), removeItem: k => m.delete(k), m }
}

// A fake agentd /decision-graphs for one user.
function fakeServer({ failCreates = 0 } = {}) {
  const rows = new Map()
  let n = 0
  const calls = []
  const fetchFn = async (url, init = {}) => {
    const method = init.method ?? 'GET'
    calls.push(`${method} ${url}`)
    const json = (o, status = 200) => new Response(o == null ? null : JSON.stringify(o), { status })
    const id = decodeURIComponent(url.split('/')[3] ?? '')
    if (method === 'GET' && !id) return json([...rows.values()].map(({ graph, ...r }) => r))
    if (method === 'POST') {
      if (failCreates-- > 0) return new Response('boom', { status: 500 })
      const b = JSON.parse(init.body)
      const row = { id: `dg_${++n}`, name: b.name, graph: b.graph, updated_at: new Date(1e12 + n).toISOString() }
      rows.set(row.id, row)
      return json(row, 201)
    }
    if (!rows.has(id)) return new Response('decision graph not found', { status: 404 })
    if (method === 'GET') return json(rows.get(id))
    if (method === 'PUT') {
      const b = JSON.parse(init.body)
      rows.set(id, { ...rows.get(id), name: b.name, graph: b.graph })
      return json(rows.get(id))
    }
    if (method === 'DELETE') { rows.delete(id); return new Response(null, { status: 204 }) }
  }
  return { fetchFn, rows, calls }
}

test('server body keeps the editor document and a name', () => {
  const g = exampleGraph()
  const body = toServerBody({ ...g, name: '  ' })
  assert.equal(body.name, 'Untitled graph')
  assert.deepEqual(Object.keys(body.graph), ['root', 'nodes'])
  const back = fromServer({ id: 'dg_1', name: 'x', graph: body.graph, updated_at: '2026-11-12T00:00:00Z' })
  assert.equal(back.id, 'dg_1')
  assert.deepEqual(back.nodes, g.nodes)
  assert.deepEqual(toApiGraph(back), toApiGraph(g))
})

test('serverStore creates, saves, lists and removes', async () => {
  const srv = fakeServer()
  const store = serverStore(srv.fetchFn)
  const g = await store.create(exampleGraph())
  assert.equal(g.id, 'dg_1')
  g.name = 'Renamed'
  await store.save(g)
  assert.deepEqual((await store.list()).map(x => x.name), ['Renamed'])
  assert.equal((await store.get('dg_1')).nodes.length, 3)
  await store.remove('dg_1')
  await assert.rejects(store.get('dg_1'), e => e.status === 404 && /not found/.test(e.message))
})

test('browser graphs move to the server once, and failures stay local', async () => {
  const storage = memStorage()
  saveGraphs(storage, [exampleGraph(), blankGraph('Second')])
  const srv = fakeServer({ failCreates: 1 })
  const first = await moveLocalToServer(storage, serverStore(srv.fetchFn))
  assert.deepEqual(first, { moved: 1, left: 1 })
  assert.equal(loadGraphs(storage).length, 1)
  const second = await moveLocalToServer(storage, serverStore(srv.fetchFn))
  assert.deepEqual(second, { moved: 1, left: 0 })
  assert.equal(storage.getItem(STORAGE_KEY), null)
  assert.equal(srv.rows.size, 2)
})

test('localStore keeps graphs in the browser', async () => {
  const store = localStore(memStorage())
  const g = await store.create(exampleGraph())
  await store.save({ ...g, name: 'Local' })
  assert.deepEqual((await store.list()).map(x => x.name), ['Local'])
  await store.remove(g.id)
  assert.deepEqual(await store.list(), [])
})
