// Helpers for the decision-model composer. Pure functions, tested with
// `node --test`.
//
// A decision model (e.g. Liquid d1) answers named questions about a state
// with probabilities. Question types: "noul" (yes/no), "choice" (one of
// named options) and "score" (a level on an ordered scale, lowest first).

export const QUESTION_TYPES = [
  { value: 'noul',   label: 'Yes / no' },
  { value: 'choice', label: 'Choice' },
  { value: 'score',  label: 'Score' },
]

export function blankQuestion(n = 1) {
  return { name: '', type: 'noul', instructions: '', options: '', levels: '', _id: `q${n}-${Math.random().toString(36).slice(2, 7)}` }
}

// "Billing: payments and refunds" → ["billing", "payments and refunds"].
// Options are one per line, or comma separated when on a single line.
function splitList(text) {
  const t = (text ?? '').trim()
  if (!t) return []
  const parts = t.includes('\n') ? t.split('\n') : t.split(',')
  return parts.map(s => s.trim()).filter(Boolean)
}

export function slug(text, fallback = 'q') {
  const s = (text ?? '').toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '').slice(0, 40)
  return s || fallback
}

// Turn the editor's questions into API questions. Throws an Error with a
// message fit to show when something is missing.
export function toApiQuestions(rows) {
  if (!rows.length) throw new Error('Add at least one question.')
  const used = new Set()
  return rows.map((row, i) => {
    const instructions = (row.instructions ?? '').trim()
    if (!instructions) throw new Error(`Question ${i + 1} needs its question text.`)
    let name = slug(row.name || instructions, `q${i + 1}`)
    for (let n = 2; used.has(name); n++) name = `${slug(row.name || instructions, `q${i + 1}`)}_${n}`
    used.add(name)
    const q = { name, type: row.type, instructions }
    if (row.type === 'choice') {
      const opts = splitList(row.options)
      if (opts.length < 2) throw new Error(`Question ${i + 1} needs at least two options.`)
      const criteria = {}
      for (const o of opts) {
        const idx = o.indexOf(':')
        const key = (idx > 0 ? o.slice(0, idx) : o).trim()
        const desc = idx > 0 ? o.slice(idx + 1).trim() : ''
        criteria[key] = desc || null
      }
      if (Object.keys(criteria).length < 2) throw new Error(`Question ${i + 1} has duplicate options.`)
      q.criteria = criteria
    } else if (row.type === 'score') {
      const levels = splitList(row.levels)
      if (levels.length < 2 || levels.length > 10) throw new Error(`Question ${i + 1} needs 2 to 10 levels, lowest first.`)
      q.criteria = levels
    }
    return q
  })
}

// State is sent as JSON when it parses as an object or array, else as text.
export function toApiState(text) {
  const t = (text ?? '').trim()
  if (!t) throw new Error('Describe the situation to decide on.')
  if (t.startsWith('{') || t.startsWith('[')) {
    try { return JSON.parse(t) } catch { /* plain text that happens to start with a brace */ }
  }
  return t
}

export function pct(p) {
  if (typeof p !== 'number' || Number.isNaN(p)) return '–'
  return `${Math.round(p * 100)}%`
}

// One-line summary of an answer, e.g. "yes (98%)", "technical (97%)",
// "Major · 2.58".
export function summarize(answer) {
  const pick = answer.options?.find(o => o.key === answer.pick)
  switch (answer.type) {
    case 'noul':
      return `${answer.pick === 'true' ? 'yes' : 'no'} (${pct(answer.value)} yes)`
    case 'choice':
      return pick ? `${pick.label} (${pct(pick.probability)})` : (answer.pick ?? '–')
    case 'score':
      return pick ? `${pick.label} · ${answer.value?.toFixed?.(2) ?? '–'}` : `${answer.value ?? '–'}`
    default:
      return answer.type ? `${answer.type} answer` : 'answer'
  }
}
