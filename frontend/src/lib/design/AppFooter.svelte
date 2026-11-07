<script>
	import BetaTag from './BetaTag.svelte';
	const year = new Date().getFullYear();
</script>

<footer class="app-footer">
	<div class="inner">
		<div class="brand">
			<img class="mark" src="/brand/mark.svg" alt="" />
			<img class="wordmark" src="/brand/wordmark.svg" alt="Icomic" />
			<BetaTag />
		</div>
		<nav aria-label="Links">
			<a href="https://github.com/Mani212005/icomic/tree/main/docs" target="_blank" rel="noopener noreferrer">Docs</a>
			<a href="https://github.com/Mani212005/icomic#readme" target="_blank" rel="noopener noreferrer">README</a>
		</nav>
		<p class="copyright">&copy; {year} Icomic</p>
	</div>
</footer>

<style>
	.app-footer {
		position: relative; flex: none; margin-top: auto; padding: 28px 0 32px;
		border-top: 1px solid var(--ink); box-shadow: 0 -3px 0 var(--accent);
		background: linear-gradient(#FCFAF7e6, #FCFAF7d9), var(--paper-texture) center / cover;
	}
	.inner {
		max-width: 1440px; width: calc(100% - 80px); margin: 0 auto;
		display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 16px 32px;
	}
	.brand { display: flex; align-items: center; gap: 12px; }
	.mark {
		display: block; width: 40px; height: 40px; padding: 2px; border: 1px solid var(--ink);
		border-radius: 11px 7px 10px 6px; background: var(--surface); box-shadow: 2px 3px 0 var(--accent);
	}
	.wordmark { display: block; width: 100px; height: 40px; }
	nav { display: flex; align-items: center; gap: 8px; }
	a {
		display: inline-flex; align-items: center; min-height: 44px; padding: 0 12px; color: var(--ink);
		font-weight: 650; text-decoration: underline; text-decoration-color: var(--primary);
		text-decoration-thickness: 2px; text-underline-offset: 5px;
	}
	a:hover { background: var(--accent-soft); border-radius: 4px 6px 4px 5px; }
	.copyright { margin: 0; color: var(--muted); font-size: 12px; }
	@media (max-width: 1328px) { .inner { width: calc(100% - 64px); } }
	@media (max-width: 700px) {
		.app-footer { padding: 24px 0 28px; }
		.inner { width: calc(100% - 32px); flex-direction: column; align-items: flex-start; gap: 8px; }
		nav { margin-left: -12px; }
	}
</style>
