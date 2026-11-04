<script>
  import { icons } from './icons.js';
  export let name;
  export let label = undefined;
  export let size = 24;
</script>

<svg xmlns="http://www.w3.org/2000/svg" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden={label ? undefined : 'true'} aria-label={label} role={label ? 'img' : undefined} focusable="false" class="ink-icon">
  {#each icons[name] || icons.info as d, i}
    <path {d} stroke-width={i ? 1.5 : 1.8} />
  {/each}
</svg>

<style>
  svg { display: inline-block; flex: none; vertical-align: middle; }
</style>
