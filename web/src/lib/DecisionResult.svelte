<script>
  // Renders one decision: the state that was judged and each question's
  // answer with a probability bar per outcome. Answers of kinds we don't
  // normalise (typed answers) are shown as raw JSON.
  import { pct, summarize } from './decision/form.js'

  let { entry } = $props()  // { state, questions, model, result }

  const questionText = name => entry.questions?.find(q => q.name === name)?.instructions ?? name
</script>

<div class="decision">
  <div class="d-head">
    <span class="d-tag">decision</span>
    <span class="d-model">{entry.model}</span>
    {#if entry.result?.usage}
      <span class="d-usage">{entry.result.usage.input_tokens} input tokens</span>
    {/if}
  </div>
  {#if entry.state}<div class="d-state">{entry.state}</div>{/if}

  {#each entry.result?.answers ?? [] as a (a.name)}
    <div class="answer">
      <div class="a-q">{questionText(a.name)}</div>
      <div class="a-pick">
        {summarize(a)}
        {#if a.confidence != null}<span class="a-conf" title="How strongly the probabilities favour one answer; not the chance it is right">confidence {pct(a.confidence)}</span>{/if}
      </div>
      {#if a.options?.length}
        <div class="bars">
          {#each a.options as o (o.key)}
            <div class="bar-row" class:picked={o.key === a.pick}>
              <span class="bar-label" title={o.label}>{o.label}</span>
              <span class="bar"><span class="fill" style="width: {Math.max(0, Math.min(1, o.probability)) * 100}%"></span></span>
              <span class="bar-pct">{pct(o.probability)}</span>
            </div>
          {/each}
        </div>
      {:else}
        <pre class="a-raw">{JSON.stringify(a.raw, null, 2)}</pre>
      {/if}
    </div>
  {/each}
</div>

<style>
  .decision {
    border: 1px solid var(--border); border-radius: 10px; background: var(--surface);
    padding: 0.7rem 0.9rem; display: flex; flex-direction: column; gap: 0.6rem;
    max-width: 640px;
  }
  .d-head { display: flex; gap: 0.5rem; align-items: baseline; font-size: 0.72rem; color: var(--text4); }
  .d-tag {
    text-transform: uppercase; letter-spacing: 0.08em; font-weight: 700;
    color: var(--accent);
  }
  .d-model { font-family: monospace; }
  .d-usage { margin-left: auto; }
  .d-state {
    font-size: 0.85rem; color: var(--text2); white-space: pre-wrap;
    border-left: 2px solid var(--border); padding-left: 0.6rem;
    max-height: 8rem; overflow: auto;
  }
  .answer { display: flex; flex-direction: column; gap: 0.25rem; }
  .a-q { font-size: 0.82rem; color: var(--text3); }
  .a-pick { font-size: 0.92rem; color: var(--text); font-weight: 500; display: flex; gap: 0.6rem; align-items: baseline; }
  .a-conf { font-size: 0.72rem; color: var(--text4); font-weight: 400; }
  .bars { display: flex; flex-direction: column; gap: 0.15rem; margin-top: 0.1rem; }
  .bar-row { display: grid; grid-template-columns: minmax(4rem, 10rem) 1fr 3rem; gap: 0.5rem; align-items: center; font-size: 0.75rem; color: var(--text3); }
  .bar-row.picked { color: var(--text); }
  .bar-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .bar { height: 6px; background: var(--bg); border-radius: 3px; overflow: hidden; }
  .fill { display: block; height: 100%; background: var(--text4); border-radius: 3px; }
  .bar-row.picked .fill { background: var(--accent); }
  .bar-pct { text-align: right; font-variant-numeric: tabular-nums; }
  .a-raw { font-size: 0.72rem; color: var(--text3); background: var(--bg); padding: 0.4rem; border-radius: 6px; overflow: auto; margin: 0; }
</style>
