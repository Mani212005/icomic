<script>
	export let project = { title: 'Untitled Comic', chapters: [] };
	export let activeStage = 'script';
</script>

<div class="comic-editor">
	<div class="stage-stepper">
		{#each ['script', 'characters', 'chapters', 'draft', 'line_art', 'colour', 'export'] as stage}
			<div class="step" class:active={activeStage === stage}>
				<span class="label">{stage.replace('_', ' ').toUpperCase()}</span>
			</div>
		{/each}
	</div>

	<main class="editor-content">
		<h2>{project.title}</h2>
		<p class="status">Current Stage: <strong>{activeStage}</strong></p>
		<slot />
	</main>
</div>

<style>
	.comic-editor {
		font-family: system-ui, -apple-system, sans-serif;
		color: #1e293b;
	}
	.stage-stepper {
		display: flex;
		justify-content: space-between;
		background: #f8fafc;
		border-radius: 8px;
		padding: 0.5rem;
		margin-bottom: 1.5rem;
		overflow-x: auto;
	}
	.step {
		padding: 0.5rem 1rem;
		border-radius: 6px;
		font-size: 0.875rem;
		font-weight: 500;
		color: #64748b;
	}
	.step.active {
		background: #0284c7;
		color: #ffffff;
	}
	.editor-content {
		background: #ffffff;
		border: 1px solid #e2e8f0;
		border-radius: 8px;
		padding: 1.5rem;
	}
</style>
