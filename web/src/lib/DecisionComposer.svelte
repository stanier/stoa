<script>
  // Composer for decision models (e.g. Liquid d1). Instead of a chat message
  // you describe a situation and ask one or more questions; the model answers
  // each with probabilities. Results go back to Chat via onResult.
  import { apiFetch } from './utils.js'
  import { QUESTION_TYPES, blankQuestion, toApiQuestions, toApiState } from './decision/form.js'

  let { provider, model, onResult, disabled = false } = $props()

  let stateText = $state('')
  let rows      = $state([blankQuestion(1)])
  let busy      = $state(false)
  let error     = $state(null)

  function addRow()      { rows = [...rows, blankQuestion(rows.length + 1)] }
  function removeRow(id) { rows = rows.filter(r => r._id !== id) }

  async function ask() {
    error = null
    let questions, apiState
    try {
      questions = toApiQuestions(rows)
      apiState = toApiState(stateText)
    } catch (e) {
      error = e.message
      return
    }
    busy = true
    try {
      const res = await apiFetch('/api/decisions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider, model, state: apiState, questions }),
      })
      const text = await res.text()
      let body = null
      try { body = JSON.parse(text) } catch {}
      if (!res.ok) {
        error = body?.error ?? (text.trim() || `Request failed (${res.status})`)
        return
      }
      onResult?.({ state: stateText.trim(), questions, provider: body.provider, model: body.model, result: body.result })
      stateText = ''
    } catch (e) {
      error = 'Could not reach the decision model.'
    } finally {
      busy = false
    }
  }

  function onKeydown(e) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); ask() }
  }
</script>

<div class="decision-composer">
  <div class="questions">
    {#each rows as row, i (row._id)}
      <div class="q-row">
        <select bind:value={row.type} title="Question type">
          {#each QUESTION_TYPES as t}<option value={t.value}>{t.label}</option>{/each}
        </select>
        <input class="q-text" bind:value={row.instructions} placeholder={`Question ${i + 1}, e.g. "Is this request urgent?"`} />
        {#if row.type === 'choice'}
          <input class="q-extra" bind:value={row.options} placeholder="Options: billing, technical, sales" title="Comma separated, or one per line as name: description" />
        {:else if row.type === 'score'}
          <input class="q-extra" bind:value={row.levels} placeholder="Levels, lowest first: low, medium, high" />
        {/if}
        {#if rows.length > 1}
          <button class="icon" onclick={() => removeRow(row._id)} title="Remove question">✕</button>
        {/if}
      </div>
    {/each}
    <button class="add" onclick={addRow} disabled={rows.length >= 64}>+ Add question</button>
  </div>

  {#if error}<div class="d-error">{error}</div>{/if}

  <div class="state-row">
    <textarea bind:value={stateText} onkeydown={onKeydown} rows="2"
      placeholder="Situation to decide on: text, or JSON.  (Ctrl+Enter to ask)"
      disabled={disabled || busy}></textarea>
    <button class="ask" onclick={ask} disabled={disabled || busy || !stateText.trim()}>
      {busy ? 'Asking…' : 'Decide'}
    </button>
  </div>
</div>

<style>
  .decision-composer {
    display: flex; flex-direction: column; gap: 0.45rem;
    padding: 0.6rem 1.25rem 0.75rem; background: var(--surface); flex-shrink: 0;
  }
  .questions { display: flex; flex-direction: column; gap: 0.35rem; }
  .q-row { display: flex; gap: 0.4rem; align-items: center; }
  select, input {
    background: var(--bg); border: 1px solid var(--border); border-radius: 6px;
    color: var(--text); font: inherit; font-size: 0.82rem; padding: 0.32rem 0.5rem; outline: none;
  }
  select { flex-shrink: 0; }
  .q-text  { flex: 2; min-width: 0; }
  .q-extra { flex: 1.4; min-width: 0; }
  .icon, .add {
    background: none; border: none; color: var(--text4); cursor: pointer; font-size: 0.78rem;
  }
  .add { align-self: flex-start; padding: 0.1rem 0; }
  .add:hover, .icon:hover { color: var(--text2); }
  .d-error { color: var(--danger-light); font-size: 0.8rem; }
  .state-row { display: flex; gap: 0.5rem; }
  textarea {
    flex: 1; background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); font: inherit; font-size: 0.9rem; padding: 0.5rem 0.75rem;
    resize: vertical; min-height: 42px; max-height: 200px; outline: none; line-height: 1.45;
  }
  .ask {
    background: var(--accent); color: var(--bg); border: none; border-radius: 8px;
    padding: 0 1.1rem; font-size: 0.9rem; font-weight: 500; cursor: pointer;
    align-self: flex-end; height: 42px;
  }
  .ask:hover:not(:disabled) { background: var(--accent-light); }
  .ask:disabled { opacity: 0.4; cursor: not-allowed; }
</style>
