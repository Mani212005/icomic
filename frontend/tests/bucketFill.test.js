import test from 'node:test';
import assert from 'node:assert/strict';
import { hexToRgb, isBoundaryPixel, floodFill } from '../src/lib/colour/bucketFill.js';

test('hexToRgb parses hex colors correctly', () => {
	assert.deepEqual(hexToRgb('#4A90E2'), { r: 74, g: 144, b: 226 });
	assert.deepEqual(hexToRgb('#000000'), { r: 0, g: 0, b: 0 });
	assert.deepEqual(hexToRgb('#ffffff'), { r: 255, g: 255, b: 255 });
});

test('isBoundaryPixel identifies dark pixels as line art boundaries', () => {
	assert.equal(isBoundaryPixel(0, 0, 0), true); // Solid black
	assert.equal(isBoundaryPixel(30, 30, 30), true); // Near black anti-aliased
	assert.equal(isBoundaryPixel(255, 255, 255), false); // White fill area
});

test('floodFill fills contiguous area within boundaries', () => {
	const width = 4;
	const height = 4;
	// 4x4 image: middle 2x2 is white, border is black
	const data = new Uint8ClampedArray(width * height * 4);
	for (let y = 0; y < height; y++) {
		for (let x = 0; x < width; x++) {
			const idx = (y * width + x) * 4;
			if (x === 0 || x === 3 || y === 0 || y === 3) {
				// Black border
				data[idx] = 0;
				data[idx + 1] = 0;
				data[idx + 2] = 0;
				data[idx + 3] = 255;
			} else {
				// White interior
				data[idx] = 255;
				data[idx + 1] = 255;
				data[idx + 2] = 255;
				data[idx + 3] = 255;
			}
		}
	}

	const img = { width, height, data };
	const filled = floodFill(img, 1, 1, '#FF0000'); // Fill interior with red

	// Center pixel (1,1) should now be red
	const centerIdx = (1 * width + 1) * 4;
	assert.equal(filled.data[centerIdx], 255);
	assert.equal(filled.data[centerIdx + 1], 0);
	assert.equal(filled.data[centerIdx + 2], 0);

	// Boundary pixel (0,0) should still be black
	assert.equal(filled.data[0], 0);
	assert.equal(filled.data[1], 0);
	assert.equal(filled.data[2], 0);
});
