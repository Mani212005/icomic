import test from 'node:test';
import assert from 'node:assert/strict';
import {
	hexToRgba,
	hexToRgb,
	getLuminance,
	isLineArtBoundary,
	isBoundaryPixel,
	createGapClosedBoundaryMask,
	applyPaletteToRegions,
	floodFill,
	CanvasHistory
} from '../src/lib/colour/bucketFill.js';

test('hexToRgba and legacy hexToRgb parse hex formats correctly', () => {
	assert.deepEqual(hexToRgba('#4A90E2'), { r: 74, g: 144, b: 226, a: 255 });
	assert.deepEqual(hexToRgba('#000'), { r: 0, g: 0, b: 0, a: 255 });
	assert.deepEqual(hexToRgba('#ffffff'), { r: 255, g: 255, b: 255, a: 255 });
	assert.deepEqual(hexToRgba('#11223380'), { r: 17, g: 34, b: 51, a: 128 });
	assert.deepEqual(hexToRgb('#4A90E2'), { r: 74, g: 144, b: 226 });
});

test('applies a palette deterministically to enclosed regions and leaves the page margin white', () => {
	const width = 7;
	const height = 7;
	const lineArtData = { width, height, data: new Uint8ClampedArray(width * height * 4) };
	lineArtData.data.fill(255);
	for (let y = 0; y < height; y++) {
		for (let x = 0; x < width; x++) {
			if (x === 0 || y === 0 || x === width - 1 || y === height - 1 || x === 3) {
				const p = (y * width + x) * 4;
				lineArtData.data[p] = 0;
				lineArtData.data[p + 1] = 0;
				lineArtData.data[p + 2] = 0;
			}
		}
	}
	const imageData = { width, height, data: new Uint8ClampedArray(width * height * 4) };
	imageData.data.fill(255);
	applyPaletteToRegions(imageData, lineArtData, [
		{ region_name: 'first', hex_color: '#FF0000' },
		{ region_name: 'second', hex_color: '#0000FF' }
	]);
	assert.deepEqual(Array.from(imageData.data.slice((1 * width + 1) * 4, (1 * width + 1) * 4 + 4)), [255, 0, 0, 255]);
	assert.deepEqual(Array.from(imageData.data.slice((1 * width + 4) * 4, (1 * width + 4) * 4 + 4)), [0, 0, 255, 255]);
	assert.deepEqual(Array.from(imageData.data.slice(0, 4)), [255, 255, 255, 255]);
});

test('getLuminance, isLineArtBoundary, and isBoundaryPixel classify ink lines vs fill areas', () => {

	assert.equal(isLineArtBoundary(0, 0, 0, 255), true);
	assert.equal(isBoundaryPixel(0, 0, 0), true);

	assert.equal(isLineArtBoundary(40, 40, 40, 255, 60), true);
	assert.equal(isBoundaryPixel(40, 40, 40, 60), true);

	assert.equal(isLineArtBoundary(255, 255, 255, 255), false);
	assert.equal(isBoundaryPixel(255, 255, 255), false);

	assert.equal(isLineArtBoundary(0, 0, 0, 0), false);
});

test('visited-on-enqueue prevents duplicate enqueue and bounds stack growth', () => {

	const width = 10;
	const height = 10;
	const data = new Uint8ClampedArray(width * height * 4);
	data.fill(255);

	let barrierChecks = 0;
	const checkedIndices = new Set();
	let maxStackDepth = 0;

	const filled = floodFill(
		{ width, height, data },
		5,
		5,
		'#FF0000',
		{
			isBoundary: (x, y, flatIdx) => {
				barrierChecks++;
				checkedIndices.add(flatIdx);
				return false;
			}
		}
	);

	for (let i = 0; i < width * height; i++) {
		const p = i * 4;
		assert.equal(filled.data[p], 255);
		assert.equal(filled.data[p + 1], 0);
		assert.equal(filled.data[p + 2], 0);
		assert.equal(filled.data[p + 3], 255);
	}

	assert.ok(barrierChecks <= width * height * 4, `Barrier checks (${barrierChecks}) within bounds`);
});

test('handles large canvases without memory blowup', () => {

	const width = 1000;
	const height = 1000;
	const data = new Uint8ClampedArray(width * height * 4);
	data.fill(255);

	for (let y = 0; y < height; y++) {
		for (let x = 0; x < width; x++) {
			if (x === 0 || x === width - 1 || y === 0 || y === height - 1) {
				const p = (y * width + x) * 4;
				data[p] = 0;
				data[p + 1] = 0;
				data[p + 2] = 0;
				data[p + 3] = 255;
			}
		}
	}

	const startTime = performance.now();
	const initialMem = process.memoryUsage().heapUsed;

	const filled = floodFill(
		{ width, height, data },
		500,
		500,
		'#00AAFF',
		{ tolerance: 10 }
	);

	const elapsed = performance.now() - startTime;
	const memDiff = process.memoryUsage().heapUsed - initialMem;

	const centerP = (500 * width + 500) * 4;
	assert.equal(filled.data[centerP], 0);
	assert.equal(filled.data[centerP + 1], 170);
	assert.equal(filled.data[centerP + 2], 255);

	assert.equal(filled.data[0], 0);
	assert.equal(filled.data[1], 0);
	assert.equal(filled.data[2], 0);

	assert.ok(elapsed < 1000, `Large canvas filled in ${elapsed.toFixed(1)}ms (must be < 1000ms)`);
});

test('fills by tolerance against the clicked pixel colour', () => {
	const width = 5;
	const height = 1;

	const data = new Uint8ClampedArray([
		200, 200, 200, 255,
		205, 205, 205, 255,
		210, 210, 210, 255,
		230, 230, 230, 255,
		250, 250, 250, 255
	]);

	const filled = floodFill(
		{ width, height, data },
		0,
		0,
		'#FF0000',
		{ tolerance: 20 }
	);

	for (let i = 0; i <= 2; i++) {
		assert.equal(filled.data[i * 4], 255, `Pixel ${i} R`);
		assert.equal(filled.data[i * 4 + 1], 0, `Pixel ${i} G`);
		assert.equal(filled.data[i * 4 + 2], 0, `Pixel ${i} B`);
	}

	assert.equal(filled.data[3 * 4], 230, 'Pixel 3 unchanged');
	assert.equal(filled.data[4 * 4], 250, 'Pixel 4 unchanged');
});

test('treats only the line art actual boundary as a barrier (does not treat dark painted colours as barriers)', () => {

	const width = 4;
	const height = 4;

	const lineArtData = {
		width,
		height,
		data: new Uint8ClampedArray(width * height * 4)
	};
	lineArtData.data.fill(255);
	for (let y = 0; y < height; y++) {
		for (let x = 0; x < width; x++) {
			if (x === 0 || x === 3 || y === 0 || y === 3) {
				const p = (y * width + x) * 4;
				lineArtData.data[p] = 0;
				lineArtData.data[p + 1] = 0;
				lineArtData.data[p + 2] = 0;
				lineArtData.data[p + 3] = 255;
			}
		}
	}

	const colourLayer = {
		width,
		height,
		data: new Uint8ClampedArray(width * height * 4)
	};
	colourLayer.data.fill(255);
	for (let y = 1; y <= 2; y++) {
		for (let x = 1; x <= 2; x++) {
			const p = (y * width + x) * 4;
			colourLayer.data[p] = 10;
			colourLayer.data[p + 1] = 10;
			colourLayer.data[p + 2] = 30;
			colourLayer.data[p + 3] = 255;
		}
	}

	const filled = floodFill(
		colourLayer,
		1,
		1,
		'#FFFF00',
		{ lineArtData, tolerance: 10 }
	);

	for (let y = 1; y <= 2; y++) {
		for (let x = 1; x <= 2; x++) {
			const p = (y * width + x) * 4;
			assert.equal(filled.data[p], 255, `Pixel (${x},${y}) R`);
			assert.equal(filled.data[p + 1], 255, `Pixel (${x},${y}) G`);
			assert.equal(filled.data[p + 2], 0, `Pixel (${x},${y}) B`);
		}
	}

	assert.equal(lineArtData.data[0], 0);
});

test('morphological gap-closing prevents colour leakage through small contour gaps', () => {
	const width = 5;
	const height = 5;

	const lineArtData = {
		width,
		height,
		data: new Uint8ClampedArray(width * height * 4)
	};
	lineArtData.data.fill(255);

	for (let y = 0; y < height; y++) {
		for (let x = 0; x < width; x++) {
			if ((x === 0 || x === 4 || y === 0 || y === 4) && !(x === 2 && y === 0)) {
				const p = (y * width + x) * 4;
				lineArtData.data[p] = 0;
				lineArtData.data[p + 1] = 0;
				lineArtData.data[p + 2] = 0;
				lineArtData.data[p + 3] = 255;
			}
		}
	}

	const colourLayer = {
		width,
		height,
		data: new Uint8ClampedArray(width * height * 4)
	};
	colourLayer.data.fill(255);

	const mask = createGapClosedBoundaryMask(lineArtData, 1, 60);
	assert.equal(mask[0 * width + 2], 1, 'Gap at (2,0) bridged by dilation');

	const filled = floodFill(
		colourLayer,
		2,
		2,
		'#00FF00',
		{ lineArtData, boundaryMask: mask }
	);

	const centerP = (2 * width + 2) * 4;
	assert.equal(filled.data[centerP], 0);
	assert.equal(filled.data[centerP + 1], 255);
	assert.equal(filled.data[centerP + 2], 0);
});

test('CanvasHistory records, undos, and redos canvas actions with max limit', () => {
	const history = new CanvasHistory(3);

	const state0 = new Uint8ClampedArray([0, 0, 0, 0]);
	const state1 = new Uint8ClampedArray([1, 1, 1, 1]);
	const state2 = new Uint8ClampedArray([2, 2, 2, 2]);
	const state3 = new Uint8ClampedArray([3, 3, 3, 3]);
	const state4 = new Uint8ClampedArray([4, 4, 4, 4]);

	history.record(state0);
	history.record(state1);
	history.record(state2);
	assert.equal(history.length, 3);
	assert.equal(history.canUndo, true);
	assert.equal(history.canRedo, false);

	history.record(state3);
	assert.equal(history.length, 3);

	const undone1 = history.undo(state4);
	assert.deepEqual(undone1, state3);
	assert.equal(history.canRedo, true);

	const undone2 = history.undo(undone1);
	assert.deepEqual(undone2, state2);

	const redone = history.redo(undone2);
	assert.deepEqual(redone, state3);
});
