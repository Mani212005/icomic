<script>
	export let lineArtUrl = '';
	export let palette = [];
	export let onApprove = () => {};

	let selectedColour = palette[0]?.hex_color || '#4A90E2';
	let history = [];
	let canvas;

	function applyFill(event) {
		if (!canvas) return;
		const rect = canvas.getBoundingClientRect();
		const x = Math.floor(event.clientX - rect.left);
		const y = Math.floor(event.clientY - rect.top);
		// Stub paint bucket action
		history.push({ x, y, colour: selectedColour });
	}

	function undo() {
		history.pop();
	}
</script>

<div class="colour-editor">
	<header class="editor-header">
		<h3>Colour Editor (Client-side Flood Fill)</h3>
		<div class="actions">
			<button on:click={undo} disabled={history.length === 0}>Undo ({history.length})</button>
			<button class="primary" on:click={onApprove}>Approve Colouring</button>
		</div>
	</header>

	<div class="palette-bar">
		{#each palette as item}
			<button
				class="colour-swatch"
				style="background-color: {item.hex_color};"
				title="{item.label} ({item.region_name})"
				class:selected={selectedColour === item.hex_color}
				on:click={() => (selectedColour = item.hex_color)}
			></button>
		{/each}
	</div>

	<div class="canvas-container">
		{#if lineArtUrl}
			<!-- Line art background layer -->
			<img src={lineArtUrl} alt="Approved Line Art Layer" class="line-art-bg" style="display: none;" />
		{/if}
		<canvas
			bind:this={canvas}
			width="600"
			height="800"
			on:click={applyFill}
		></canvas>
	</div>
</div>

<style>
	.colour-editor {
		display: flex;
		flex-direction: column;
		gap: 1rem;
		border: 1px solid #e2e8f0;
		border-radius: 8px;
		padding: 1rem;
		background: #ffffff;
	}
	.editor-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}
	.palette-bar {
		display: flex;
		gap: 0.5rem;
		padding: 0.5rem;
		background: #f8fafc;
		border-radius: 6px;
	}
	.colour-swatch {
		width: 32px;
		height: 32px;
		border-radius: 4px;
		border: 2px solid transparent;
		cursor: pointer;
	}
	.colour-swatch.selected {
		border-color: #0f172a;
		transform: scale(1.1);
	}
	.canvas-container {
		display: flex;
		justify-content: center;
		background: #f1f5f9;
		padding: 1rem;
		border-radius: 6px;
	}
	canvas {
		background: #ffffff;
		box-shadow: 0 4px 6px -1px rgb(0 0 0 / 0.1);
		cursor: crosshair;
	}
</style>
