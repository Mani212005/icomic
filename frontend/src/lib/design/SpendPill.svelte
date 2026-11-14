<script>
	export let view = null;
</script>

{#if view}
	<div class="status-pill spend-pill {view.level}" title={view.title} data-testid="spend-pill">
		<span class="indicator" aria-hidden="true"></span>
		<span class="full" aria-hidden="true">{view.text}</span>
		<span class="short" aria-hidden="true">{view.short}</span>
		<span class="sr">{view.title}</span>
	</div>
{/if}

<style>
	.spend-pill.low .indicator { background: var(--warning); outline: 1px solid var(--ink); }
	.spend-pill.out { color: var(--ink); background: var(--danger-soft); border: 1px solid var(--ink); border-radius: 4px; }
	.spend-pill.out .indicator { background: var(--danger); border-radius: 0; }
	.short { display: none; }
	@media (max-width: 700px) {
		.full, .indicator { display: none; }
		.short { display: inline; }
	}
	.sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
</style>
