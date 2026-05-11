<script>
  let { checked = false, disabled = false, onchange, title = '' } = $props()
</script>

<label class="toggle" {title}>
  <input type="checkbox" {checked} {disabled} {onchange} />
  <span class="toggle-track"></span>
</label>

<style>
  .toggle { position: relative; flex-shrink: 0; width: 30px; height: 16px; margin-top: 2px; }
  .toggle input { opacity: 0; width: 0; height: 0; }
  .toggle-track {
    position: absolute; inset: 0; background: var(--border);
    border-radius: 8px; cursor: pointer; transition: background 0.2s;
  }
  .toggle input:checked + .toggle-track { background: var(--accent); }
  .toggle-track::after {
    content: ''; position: absolute; left: 2px; top: 2px;
    width: 12px; height: 12px; background: var(--text);
    border-radius: 50%; transition: transform 0.2s;
  }
  .toggle input:checked + .toggle-track::after { transform: translateX(14px); background: var(--bg); }
  .toggle input:disabled + .toggle-track { opacity: 0.4; cursor: not-allowed; }
</style>
