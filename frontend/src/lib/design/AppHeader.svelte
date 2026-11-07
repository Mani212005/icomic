<script>
	import { parallax } from './scroll.js';
	import BetaTag from './BetaTag.svelte';
</script>

<!--
	Top bar. Left to right: the `leading` slot (the hamburger), the brand mark and wordmark,
	the Beta tag, then the status pills and the `account` slot on the right.

	`account` is the reserved place for a sign-in or account control (sign-in button, avatar
	menu). It sits last, vertically centred, and gets at most 44 px of height. Leave it empty
	and the bar looks complete.
-->
<header class="app-header">
	<div class="wash" aria-hidden="true" use:parallax></div>
	<div class="bar">
		<div class="leading"><slot name="leading" /></div>
		<div class="brand">
			<img class="brand-mark" src="/brand/mark.svg" alt="" />
			<img class="brand-wordmark" src="/brand/wordmark.svg" alt="Icomic" />
			<BetaTag />
		</div>
		<div class="trailing">
			<slot name="status" />
			<div class="account"><slot name="account" /></div>
		</div>
	</div>
</header>

<style>
	.app-header {
		position: sticky; top: 0; z-index: 28; overflow: hidden; height: var(--header-h, 72px);
		border-bottom: 1px solid var(--ink); background: var(--surface); box-shadow: 0 3px 0 var(--accent);
	}
	.wash {
		position: absolute; inset: -4px 0 -32px; pointer-events: none; will-change: transform;
		background: url('/textures/wash.svg') left center / min(520px, 70%) 100% no-repeat, var(--paper-texture) center / cover;
		opacity: .55;
	}
	.bar {
		position: relative; height: 100%; padding: 0 40px 0 20px; display: flex; align-items: center; gap: 16px;
	}
	.leading { display: flex; align-items: center; min-width: 44px; }
	.brand { display: flex; align-items: center; gap: 12px; min-width: 0; }
	.brand-mark {
		display: block; width: 44px; height: 44px; padding: 2px; border: 1px solid var(--ink);
		border-radius: 12px 8px 11px 7px; background: var(--surface); box-shadow: 2px 3px 0 var(--accent);
	}
	.brand-wordmark { display: block; width: 118px; height: 44px; }
	.trailing { margin-left: auto; display: flex; align-items: center; gap: 14px; }
	.account { display: flex; align-items: center; max-height: 44px; }
	.account:empty { display: none; }
	@media (max-width: 700px) {
		.bar { padding: 0 14px 0 12px; gap: 10px; }
		.brand { gap: 8px; }
		.brand-mark { width: 36px; height: 36px; border-radius: 10px 7px 9px 6px; }
		.brand-wordmark { width: 92px; height: 36px; }
	}
</style>
