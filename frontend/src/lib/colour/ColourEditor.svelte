<script>
	import { onMount } from 'svelte';
	import {
		floodFill,
		CanvasHistory,
		hexToRgba,
		createGapClosedBoundaryMask
	} from './bucketFill.js';

	export let lineArtUrl = '';
	export let palette = [];
	export let onApprove = () => {};
	export let onPaletteChange = (newPalette) => {};

	let canvas;
	let ctx;
	let lineArtCanvas;
	let lineArtCtx;

	let selectedColour = palette[0]?.hex_color || '#38BDF8';
	let customColour = '#38BDF8';
	let activeTool = 'bucket'; // 'bucket', 'eyedropper', 'brush'
	let tolerance = 32;
	let gapClosing = false;
	let brushSize = 2;

	let isDrawing = false;
	let lastX = 0;
	let lastY = 0;

	const history = new CanvasHistory(20);
	let historyLength = 0;
	let canUndo = false;
	let canRedo = false;

	const width = 600;
	const height = 750;

	function updateHistoryState() {
		historyLength = history.length;
		canUndo = history.canUndo;
		canRedo = history.canRedo;
	}

	onMount(() => {
		ctx = canvas.getContext('2d', { willReadFrequently: true });
		lineArtCtx = lineArtCanvas.getContext('2d', { willReadFrequently: true });

		// Initialize colour canvas with white background
		ctx.fillStyle = '#FFFFFF';
		ctx.fillRect(0, 0, width, height);

		// Initialize line art canvas
		initLineArt();
		updateHistoryState();
	});

	function initLineArt() {
		lineArtCtx.fillStyle = '#FFFFFF';
		lineArtCtx.fillRect(0, 0, width, height);

		if (lineArtUrl) {
			const img = new Image();
			img.crossOrigin = 'anonymous';
			img.onload = () => {
				lineArtCtx.drawImage(img, 0, 0, width, height);
			};
			img.onerror = () => {
				drawFallbackLineArt();
			};
			img.src = lineArtUrl;
		} else {
			drawFallbackLineArt();
		}
	}

	function drawFallbackLineArt() {
		// Draw demo comic panels if no external image
		lineArtCtx.strokeStyle = '#000000';
		lineArtCtx.lineWidth = 4;
		lineArtCtx.fillStyle = '#000000';

		// Outer border
		lineArtCtx.strokeRect(20, 20, width - 40, height - 40);

		// Top panel
		lineArtCtx.strokeRect(30, 30, width - 60, 320);

		// Bottom left panel
		lineArtCtx.strokeRect(30, 370, (width - 70) / 2, 340);

		// Bottom right panel
		lineArtCtx.strokeRect(30 + (width - 70) / 2 + 10, 370, (width - 70) / 2, 340);

		// Detective silhouette outline in top panel
		lineArtCtx.beginPath();
		lineArtCtx.arc(150, 150, 40, 0, Math.PI * 2);
		lineArtCtx.stroke();

		lineArtCtx.beginPath();
		lineArtCtx.moveTo(150, 190);
		lineArtCtx.lineTo(100, 330);
		lineArtCtx.lineTo(200, 330);
		lineArtCtx.closePath();
		lineArtCtx.stroke();
	}

	function handleCanvasClick(e) {
		if (activeTool === 'eyedropper') {
			pickColour(e);
		} else if (activeTool === 'bucket') {
			applyBucketFill(e);
		}
	}

	function getCanvasCoords(e) {
		const rect = canvas.getBoundingClientRect();
		const scaleX = canvas.width / rect.width;
		const scaleY = canvas.height / rect.height;
		return {
			x: Math.floor((e.clientX - rect.left) * scaleX),
			y: Math.floor((e.clientY - rect.top) * scaleY)
		};
	}

	function applyBucketFill(e) {
		const { x, y } = getCanvasCoords(e);
		if (x < 0 || x >= width || y < 0 || y >= height) return;

		const currentImgData = ctx.getImageData(0, 0, width, height);
		const lineArtImgData = lineArtCtx.getImageData(0, 0, width, height);

		// Record current state for undo
		history.record(currentImgData.data);
		updateHistoryState();

		// Apply visited-on-enqueue flood fill with line art barrier
		const filled = floodFill(
			currentImgData,
			x,
			y,
			selectedColour,
			{
				lineArtData: lineArtImgData,
				tolerance,
				darknessThreshold: 60,
				gapClosingRadius: gapClosing ? 1 : 0
			}
		);

		ctx.putImageData(filled, 0, 0);
	}

	function pickColour(e) {
		const { x, y } = getCanvasCoords(e);
		const pixel = ctx.getImageData(x, y, 1, 1).data;
		const hex = `#${((1 << 24) + (pixel[0] << 16) + (pixel[1] << 8) + pixel[2]).toString(16).slice(1)}`;
		selectedColour = hex;
		customColour = hex;
		activeTool = 'bucket'; // switch back to bucket after sampling
	}

	function handleMouseDown(e) {
		if (activeTool !== 'brush') return;
		isDrawing = true;
		const coords = getCanvasCoords(e);
		lastX = coords.x;
		lastY = coords.y;

		// Record for undo before stroke
		const currentImgData = lineArtCtx.getImageData(0, 0, width, height);
		history.record(ctx.getImageData(0, 0, width, height).data);
		updateHistoryState();
	}

	function handleMouseMove(e) {
		if (!isDrawing || activeTool !== 'brush') return;
		const { x, y } = getCanvasCoords(e);

		// Draw ink bridge on line art layer to close gaps
		lineArtCtx.strokeStyle = '#000000';
		lineArtCtx.lineWidth = brushSize;
		lineArtCtx.lineCap = 'round';
		lineArtCtx.beginPath();
		lineArtCtx.moveTo(lastX, lastY);
		lineArtCtx.lineTo(x, y);
		lineArtCtx.stroke();

		lastX = x;
		lastY = y;
	}

	function handleMouseUp() {
		isDrawing = false;
	}

	function handleUndo() {
		if (!canUndo) return;
		const currentData = ctx.getImageData(0, 0, width, height).data;
		const previousData = history.undo(currentData);
		if (previousData) {
			const imgData = ctx.createImageData(width, height);
			imgData.data.set(previousData);
			ctx.putImageData(imgData, 0, 0);
			updateHistoryState();
		}
	}

	function handleRedo() {
		if (!canRedo) return;
		const currentData = ctx.getImageData(0, 0, width, height).data;
		const nextData = history.redo(currentData);
		if (nextData) {
			const imgData = ctx.createImageData(width, height);
			imgData.data.set(nextData);
			ctx.putImageData(imgData, 0, 0);
			updateHistoryState();
		}
	}

	function addCustomColour() {
		if (!palette.some((p) => p.hex_color.toLowerCase() === customColour.toLowerCase())) {
			const updated = [
				...palette,
				{ region_name: 'custom', hex_color: customColour, label: 'Custom' }
			];
			palette = updated;
			onPaletteChange(updated);
		}
		selectedColour = customColour;
	}
</script>

<div class="colour-studio">
	<header class="toolbar">
		<div class="tool-group">
			<button
				class="tool-btn"
				class:active={activeTool === 'bucket'}
				on:click={() => (activeTool = 'bucket')}
				title="Paint Bucket Tool (Client-side Flood Fill)"
			>
				🪣 Bucket Fill
			</button>
			<button
				class="tool-btn"
				class:active={activeTool === 'eyedropper'}
				on:click={() => (activeTool = 'eyedropper')}
				title="Eyedropper Sample Tool"
			>
				💉 Eyedropper
			</button>
			<button
				class="tool-btn"
				class:active={activeTool === 'brush'}
				on:click={() => (activeTool = 'brush')}
				title="Gap Closer Brush (Draw line art bridge strokes)"
			>
				✏️ Gap Closer
			</button>
		</div>

		<div class="settings-group">
			<label class="setting-item" title="Tolerance for colour fill against clicked pixel">
				<span>Tolerance: {tolerance}</span>
				<input type="range" min="0" max="100" bind:value={tolerance} />
			</label>
			<label class="setting-item checkbox" title="Enable morphological gap-closing to bridge tiny line art gaps">
				<input type="checkbox" bind:checked={gapClosing} />
				<span>Auto Gap-Close</span>
			</label>
		</div>

		<div class="history-group">
			<button class="tool-btn" on:click={handleUndo} disabled={!canUndo} title="Undo last fill (Ctrl+Z)">
				↩ Undo ({historyLength})
			</button>
			<button class="tool-btn" on:click={handleRedo} disabled={!canRedo} title="Redo (Ctrl+Y)">
				↪ Redo
			</button>
		</div>

		<div class="action-group">
			<button class="approve-btn" on:click={onApprove}>
				✓ Approve Colouring
			</button>
		</div>
	</header>

	<div class="palette-bar">
		<div class="swatches">
			{#each palette as item}
				<button
					class="colour-swatch"
					style="background-color: {item.hex_color};"
					class:selected={selectedColour.toLowerCase() === item.hex_color.toLowerCase()}
					title="{item.label} ({item.region_name || 'Palette'}) - {item.hex_color}"
					on:click={() => (selectedColour = item.hex_color)}
				>
					<span class="swatch-label">{item.region_name || item.label}</span>
				</button>
			{/each}
		</div>

		<div class="custom-picker">
			<input type="color" bind:value={customColour} on:change={addCustomColour} title="Choose custom color" />
			<button class="small-btn" on:click={addCustomColour}>Set Color</button>
		</div>
	</div>

	<div class="workspace-area">
		<div class="canvas-viewport">
			<!-- Colour Layer (underneath) -->
			<canvas
				bind:this={canvas}
				{width}
				{height}
				class="active-canvas"
				on:click={handleCanvasClick}
				on:mousedown={handleMouseDown}
				on:mousemove={handleMouseMove}
				on:mouseup={handleMouseUp}
				on:mouseleave={handleMouseUp}
			></canvas>

			<!-- Line Art Layer (composited on top with multiply blend mode) -->
			<canvas
				bind:this={lineArtCanvas}
				{width}
				{height}
				class="line-art-overlay"
			></canvas>
		</div>
	</div>
</div>

<style>
	.colour-studio {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
		background: #ffffff;
		border: 1px solid #e2e8f0;
		border-radius: 8px;
		padding: 1rem;
		box-shadow: 0 1px 3px rgba(0, 0, 0, 0.05);
	}
	.toolbar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		padding-bottom: 0.75rem;
		border-bottom: 1px solid #e2e8f0;
	}
	.tool-group,
	.settings-group,
	.history-group,
	.action-group {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.tool-btn {
		background: #f8fafc;
		border: 1px solid #cbd5e1;
		padding: 0.4rem 0.75rem;
		border-radius: 6px;
		font-size: 0.875rem;
		font-weight: 500;
		cursor: pointer;
		color: #1e293b;
		transition: all 0.15s ease;
	}
	.tool-btn:hover:not(:disabled) {
		background: #f1f5f9;
		border-color: #94a3b8;
	}
	.tool-btn.active {
		background: #0284c7;
		color: #ffffff;
		border-color: #0284c7;
	}
	.tool-btn:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}
	.setting-item {
		display: flex;
		align-items: center;
		gap: 0.35rem;
		font-size: 0.8125rem;
		color: #475569;
	}
	.setting-item input[type='range'] {
		width: 70px;
	}
	.approve-btn {
		background: #16a34a;
		color: white;
		border: none;
		padding: 0.45rem 1rem;
		border-radius: 6px;
		font-weight: 600;
		font-size: 0.875rem;
		cursor: pointer;
		transition: background 0.15s ease;
	}
	.approve-btn:hover {
		background: #15803d;
	}
	.palette-bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		padding: 0.5rem 0.75rem;
		background: #f8fafc;
		border-radius: 6px;
		border: 1px solid #e2e8f0;
	}
	.swatches {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.colour-swatch {
		min-width: 50px;
		height: 36px;
		padding: 0 0.5rem;
		border-radius: 6px;
		border: 2px solid transparent;
		cursor: pointer;
		display: flex;
		align-items: center;
		justify-content: center;
		box-shadow: 0 1px 2px rgba(0, 0, 0, 0.1);
		transition: transform 0.1s ease, border-color 0.1s ease;
	}
	.colour-swatch.selected {
		border-color: #0f172a;
		transform: scale(1.08);
		box-shadow: 0 2px 4px rgba(0, 0, 0, 0.2);
	}
	.swatch-label {
		font-size: 0.7rem;
		font-weight: 600;
		color: #ffffff;
		text-shadow: 0 1px 2px rgba(0, 0, 0, 0.8);
		text-transform: capitalize;
	}
	.custom-picker {
		display: flex;
		align-items: center;
		gap: 0.35rem;
	}
	.small-btn {
		background: #e2e8f0;
		border: 1px solid #cbd5e1;
		font-size: 0.75rem;
		padding: 0.25rem 0.5rem;
		border-radius: 4px;
		cursor: pointer;
	}
	.workspace-area {
		display: flex;
		justify-content: center;
		background: #f1f5f9;
		padding: 1.5rem;
		border-radius: 6px;
		overflow: auto;
	}
	.canvas-viewport {
		position: relative;
		width: 600px;
		height: 750px;
		box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -2px rgba(0, 0, 0, 0.1);
		background: #ffffff;
	}
	.active-canvas {
		position: absolute;
		top: 0;
		left: 0;
		cursor: crosshair;
		z-index: 1;
	}
	.line-art-overlay {
		position: absolute;
		top: 0;
		left: 0;
		pointer-events: none;
		mix-blend-mode: multiply;
		z-index: 2;
	}
</style>
