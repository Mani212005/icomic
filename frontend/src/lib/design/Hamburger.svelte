<script>
	// The button that opens a Sidebar. `button` is bindable so the Sidebar can return focus to it.
	export let open = false;
	export let controls;
	export let label = 'Menu';
	export let button = null;
</script>

<button type="button" class="hamburger" class:open bind:this={button} aria-expanded={open} aria-controls={controls} aria-label={label} on:click>
	<span class="bars" aria-hidden="true"><span></span><span></span><span></span></span>
</button>

<style>
	.hamburger {
		flex: none; width: 44px; height: 44px; min-height: 44px; padding: 0;
		border-radius: 7px 5px 8px 4px; background: var(--surface);
	}
	.hamburger.open { background: var(--accent-soft); box-shadow: 2px 3px 0 #C47F6F40; }
	.bars { position: relative; display: block; width: 20px; height: 14px; }
	.bars span {
		position: absolute; left: 0; width: 100%; height: 2px; border-radius: 2px; background: var(--ink);
		transition: transform 200ms ease, opacity 150ms ease;
	}
	.bars span:nth-child(1) { top: 0; }
	.bars span:nth-child(2) { top: 6px; }
	.bars span:nth-child(3) { top: 12px; }
	.open .bars span:nth-child(1) { transform: translateY(6px) rotate(45deg); }
	.open .bars span:nth-child(2) { opacity: 0; }
	.open .bars span:nth-child(3) { transform: translateY(-6px) rotate(-45deg); }
</style>
