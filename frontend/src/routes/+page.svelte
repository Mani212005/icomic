<script>
	import ComicEditor from '$lib/editor/ComicEditor.svelte';
	import { MockApiClient } from '$lib/api/mockClient.js';
	import { AGE_TERMS_ADVISORY } from '$lib/config/policies.js';

	// Client instance
	const apiClient = new MockApiClient();

	// Demo project
	let project = {
		id: 'proj-demo-1',
		title: 'The Quantum Detective',
		art_style: 'cyberpunk-noir',
		chapter_count: 3,
		script: 'In the neon-drenched alleys of Sector 4, Detective Jack Vance pursued the enigmatic cipher that had consumed his partner.'
	};

	let activeStage = 'script';
</script>

<div class="app-layout">
	<header class="navbar">
		<div class="brand">
			<span class="logo">🎨</span>
			<div class="brand-text">
				<h1>Icomic</h1>
				<span class="tagline">Meta AI Comic Generation Studio</span>
			</div>
		</div>

		<div class="nav-status">
			<div class="status-pill global-pill" title="Users are global (no geo-restrictions)">
				<span class="indicator green"></span>
				<span>Global Access</span>
			</div>

			<div class="status-pill advisory-pill" title={AGE_TERMS_ADVISORY}>
				<span class="advisory-text">18+ Advisory</span>
			</div>

			<div class="status-pill mock-pill" title="Running against hermetic typed mock API">
				<span class="badge">Mock API</span>
			</div>
		</div>
	</header>

	<main class="main-container">
		<ComicEditor {project} {apiClient} bind:activeStage />
	</main>
</div>

<style>
	:global(body) {
		margin: 0;
		padding: 0;
		font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
		background: #f1f5f9;
		color: #0f172a;
	}
	.app-layout {
		min-height: 100vh;
		display: flex;
		flex-direction: column;
	}
	.navbar {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 0.75rem 2rem;
		background: #ffffff;
		border-bottom: 1px solid #e2e8f0;
		box-shadow: 0 1px 2px rgba(0, 0, 0, 0.03);
	}
	.brand {
		display: flex;
		align-items: center;
		gap: 0.75rem;
	}
	.logo {
		font-size: 1.75rem;
	}
	.brand-text h1 {
		margin: 0;
		font-size: 1.25rem;
		font-weight: 800;
		color: #0f172a;
		letter-spacing: -0.025em;
	}
	.tagline {
		font-size: 0.75rem;
		color: #64748b;
	}
	.nav-status {
		display: flex;
		align-items: center;
		gap: 0.75rem;
	}
	.status-pill {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		background: #f8fafc;
		border: 1px solid #e2e8f0;
		padding: 0.3rem 0.6rem;
		border-radius: 9999px;
		font-size: 0.75rem;
		font-weight: 500;
		color: #334155;
	}
	.indicator {
		width: 6px;
		height: 6px;
		border-radius: 50%;
	}
	.indicator.green {
		background: #16a34a;
	}
	.advisory-text {
		color: #64748b;
	}
	.badge {
		background: #0284c7;
		color: #ffffff;
		padding: 0.1rem 0.4rem;
		border-radius: 4px;
		font-size: 0.7rem;
		font-weight: 600;
	}
	.main-container {
		flex: 1;
		max-width: 1100px;
		width: 100%;
		margin: 1.5rem auto;
		padding: 0 1rem;
		box-sizing: border-box;
	}
</style>
