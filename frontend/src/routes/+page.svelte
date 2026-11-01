<script>
	import ComicEditor from '$lib/editor/ComicEditor.svelte';
	import ColourEditor from '$lib/colour/ColourEditor.svelte';

	let project = {
		title: 'The Quantum Detective',
		artStyle: 'cyberpunk-noir',
		chapterCount: 3
	};

	let activeStage = 'colour';
	let palette = [
		{ region_name: 'sky', hex_color: '#1E293B', label: 'Night Sky' },
		{ region_name: 'neon', hex_color: '#38BDF8', label: 'Cyan Neon' },
		{ region_name: 'coat', hex_color: '#F43F5E', label: 'Crimson Trenchcoat' }
	];

	function handleApproveColour() {
		activeStage = 'export';
	}
</script>

<div class="app-layout">
	<header class="navbar">
		<div class="brand">
			<span class="logo">🎨</span>
			<h1>Icomic</h1>
		</div>
		<div class="user-info">
			<span>Demo User (18+)</span>
		</div>
	</header>

	<div class="main-container">
		<ComicEditor {project} {activeStage}>
			{#if activeStage === 'colour'}
				<ColourEditor
					{palette}
					lineArtUrl="/mock-line-art.png"
					onApprove={handleApproveColour}
				/>
			{:else if activeStage === 'export'}
				<div class="export-preview">
					<h3>Ready for Export</h3>
					<p>All pipeline stages approved. You can download the assembled PDF below.</p>
					<button class="download-btn">Download PDF</button>
				</div>
			{/if}
		</ComicEditor>
	</div>
</div>

<style>
	:global(body) {
		margin: 0;
		padding: 0;
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
		padding: 1rem 2rem;
		background: #ffffff;
		border-bottom: 1px solid #e2e8f0;
	}
	.brand {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.logo {
		font-size: 1.5rem;
	}
	.navbar h1 {
		margin: 0;
		font-size: 1.25rem;
		font-weight: 700;
	}
	.main-container {
		flex: 1;
		max-width: 1000px;
		width: 100%;
		margin: 2rem auto;
		padding: 0 1rem;
		box-sizing: border-box;
	}
	.export-preview {
		padding: 2rem;
		text-align: center;
		background: #f8fafc;
		border-radius: 8px;
	}
	.download-btn {
		background: #0284c7;
		color: white;
		border: none;
		padding: 0.75rem 1.5rem;
		font-size: 1rem;
		border-radius: 6px;
		cursor: pointer;
		margin-top: 1rem;
	}
	.download-btn:hover {
		background: #0369a1;
	}
</style>
