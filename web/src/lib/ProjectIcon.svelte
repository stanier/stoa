<script>
  let { project, size = 13, style = 'color-dot' } = $props()
</script>

<span class="icon-wrap" style="width:{size + 4}px; height:{size + 4}px;">
  {#if style === 'color-dot'}
    <span
      class="color-dot"
      style="width:{size - 2}px; height:{size - 2}px; background:{project.color}; box-shadow:0 0 0 1px {project.color}33;"
    ></span>
  {:else if style === 'glyph'}
    <span class="glyph" style="color:{project.color}; font-size:{size}px;">{project.glyph}</span>
  {:else}
    <span class="emoji" style="font-size:{size}px;">{project.emoji}</span>
  {/if}
</span>

<style>
  .icon-wrap {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    vertical-align: middle;
  }
  .color-dot {
    border-radius: 3px;
    display: inline-block;
  }
  .glyph {
    font-family: monospace;
    line-height: 1;
  }
  .emoji {
    line-height: 1;
  }
</style>
