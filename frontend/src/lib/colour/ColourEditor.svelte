<script>
	import Icon from '$lib/design/Icon.svelte';
	import { onMount } from 'svelte';
	import { keyboardCanvas } from './keyboardCanvas.js';
	let keyboardCursor = null;
	const canvasKeyboardHandlers = {
		undo: handleUndo, redo: handleRedo,
		apply: handleCanvasClick, start: handleMouseDown, move: handleMouseMove, stop: handleMouseUp,
		isBrush: () => activeTool === 'brush', cursor: (point) => keyboardCursor = point
	};
	function handlePointerDown(event) {
		canvas.setPointerCapture(event.pointerId);
		handleMouseDown(event);
	}
	import {
		floodFill,
		CanvasHistory,
		hexToRgba,
		createGapClosedBoundaryMask,
		applyPaletteToRegions
	} from './bucketFill.js';

	export let lineArtUrl = '';
	export let palette = [];
	export let onApprove = () => {};
	export let onPaletteChange = (newPalette) => {};

	let canvas;
	let ctx;
	let lineArtCanvas;
	let lineArtCtx;
	let lineArtReady = false;
	let appliedPaletteSignature = '';

	let selectedColour = palette[0]?.hex_color || '#38BDF8';
	let customColour = '#38BDF8';
	let activeTool = 'bucket';
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
	$: paletteItems = Array.isArray(palette)
		? palette
		: Object.entries(palette || {}).map(([region_name, hex_color]) => ({ region_name, hex_color, label: region_name }));
	$: paletteSignature = JSON.stringify(paletteItems.map((item) => [item.region_name, item.hex_color]));
	$: if (lineArtReady && paletteItems.length > 0 && paletteSignature !== appliedPaletteSignature) {
		appliedPaletteSignature = paletteSignature;
		applySuggestedPalette();
	}

	function updateHistoryState() {
		historyLength = history.length;
		canUndo = history.canUndo;
		canRedo = history.canRedo;
	}

	onMount(() => {
		ctx = canvas.getContext('2d', { willReadFrequently: true });
		lineArtCtx = lineArtCanvas.getContext('2d', { willReadFrequently: true });

		ctx.fillStyle = '#FFFFFF';
		ctx.fillRect(0, 0, width, height);

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
				lineArtReady = true;
			};
			img.onerror = () => {
				drawFallbackLineArt();
				lineArtReady = true;
			};
			img.src = lineArtUrl;
		} else {
			drawFallbackLineArt();
			lineArtReady = true;
		}
	}

	function applySuggestedPalette() {
		ctx.fillStyle = '#FFFFFF';
		ctx.fillRect(0, 0, width, height);
		const coloured = applyPaletteToRegions(
			ctx.getImageData(0, 0, width, height),
			lineArtCtx.getImageData(0, 0, width, height),
			paletteItems,
			{ darknessThreshold: 60, gapClosingRadius: 1 }
		);
		ctx.putImageData(coloured, 0, 0);
	}

	function approveColour() {
		const composite = document.createElement('canvas');
		composite.width = width;
		composite.height = height;
		const compositeCtx = composite.getContext('2d');
		compositeCtx.drawImage(canvas, 0, 0);
		compositeCtx.drawImage(lineArtCanvas, 0, 0);
		composite.toBlob((blob) => {
			if (blob) onApprove(blob);
		}, 'image/png');
	}

	function drawFallbackLineArt() {

		lineArtCtx.strokeStyle = '#000000';
		lineArtCtx.lineWidth = 4;
		lineArtCtx.fillStyle = '#000000';

		lineArtCtx.strokeRect(20, 20, width - 40, height - 40);

		lineArtCtx.strokeRect(30, 30, width - 60, 320);

		lineArtCtx.strokeRect(30, 370, (width - 70) / 2, 340);

		lineArtCtx.strokeRect(30 + (width - 70) / 2 + 10, 370, (width - 70) / 2, 340);

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

		history.record(currentImgData.data);
		updateHistoryState();

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
		activeTool = 'bucket';
	}

	function handleMouseDown(e) {
		if (activeTool !== 'brush') return;
		isDrawing = true;
		const coords = getCanvasCoords(e);
		lastX = coords.x;
		lastY = coords.y;

		const currentImgData = lineArtCtx.getImageData(0, 0, width, height);
		history.record(ctx.getImageData(0, 0, width, height).data);
		updateHistoryState();
	}

	function handleMouseMove(e) {
		if (!isDrawing || activeTool !== 'brush') return;
		const { x, y } = getCanvasCoords(e);

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
				aria-pressed={activeTool === 'bucket'}
				class:active={activeTool === 'bucket'}
				on:click={() => (activeTool = 'bucket')}
			>
				<Icon name="bucket" size={18} /> Fill
			</button>
			<button
				class="tool-btn"
				class:active={activeTool === 'eyedropper'} aria-pressed={activeTool === 'eyedropper'}
				on:click={() => (activeTool = 'eyedropper')}
			>
				<Icon name="eyedropper" size={18} /> Eyedropper
			</button>
			<button
				class="tool-btn"
				class:active={activeTool === 'brush'} aria-pressed={activeTool === 'brush'}
				on:click={() => (activeTool = 'brush')}
			>
				<Icon name="gap" size={18} /> Close gaps
			</button>
		</div>

		<div class="settings-group">
			<label class="setting-item" title="Tolerance">
				<span>Tol: {tolerance}</span>
				<input type="range" min="0" max="100" bind:value={tolerance} />
			</label>
			<label class="setting-item checkbox">
				<input type="checkbox" bind:checked={gapClosing} aria-label="Auto-close gaps" />
				<span>Auto</span>
			</label>
		</div>

		<div class="history-group">
			<button class="tool-btn" on:click={handleUndo} disabled={!canUndo} title="Undo (Ctrl+Z)">
				<Icon name="undo" size={18} /> Undo ({historyLength})
			</button>
			<button class="tool-btn" on:click={handleRedo} disabled={!canRedo} title="Redo (Ctrl+Y)">
				<Icon name="redo" size={18} /> Redo
			</button>
		</div>

		<div class="action-group">
			<button class="approve-btn" on:click={approveColour} disabled={!lineArtReady}>
				<Icon name="check" size={18} /> Approve
			</button>
		</div>
	</header>

	<div class="palette-bar">
		<div class="swatches">
			{#each paletteItems as item}
				<button
					class="colour-swatch"
					style="background-color: {item.hex_color};"
					class:selected={selectedColour.toLowerCase() === item.hex_color.toLowerCase()}
					aria-pressed={selectedColour.toLowerCase() === item.hex_color.toLowerCase()}
					title="{item.label} ({item.region_name || 'Palette'}) - {item.hex_color}"
					on:click={() => (selectedColour = item.hex_color)}
				>
					<span class="swatch-label">{item.region_name || item.label}</span>
				</button>
			{/each}
		</div>

		<div class="custom-picker">
			<input type="color" bind:value={customColour} on:change={addCustomColour} aria-label="Choose color" />
			<button class="small-btn" on:click={addCustomColour} aria-label="Set color">Set</button>
		</div>
	</div>

	<div class="workspace-area">
		<div class="canvas-viewport">

			<canvas
				bind:this={canvas}
				{width}
				{height}
				class="active-canvas"
				tabindex="0"
				aria-label="Canvas: arrows move; Enter paints."
				use:keyboardCanvas={canvasKeyboardHandlers}
				on:click={handleCanvasClick}
				on:pointerdown={handlePointerDown}
				on:pointermove={handleMouseMove}
				on:pointerup={handleMouseUp}
				on:pointercancel={handleMouseUp}
			></canvas>

			<canvas
				bind:this={lineArtCanvas}
				{width}
				{height}
				class="line-art-overlay"
			></canvas>
			{#if keyboardCursor}<span class="canvas-cursor" aria-hidden="true" style="left: {keyboardCursor.x}%; top: {keyboardCursor.y}%"></span>{/if}
		</div>
	</div>
</div>
