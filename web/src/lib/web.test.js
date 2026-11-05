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
