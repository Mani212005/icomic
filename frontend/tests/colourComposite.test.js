import test from 'node:test';
import assert from 'node:assert/strict';
import { drawColourComposite } from '../src/lib/colour/colourComposite.js';

function makePixelCanvas(pixels) {
	return { pixels };
}

function makeCompositeContext() {
	const pixels = [];
	return {
		pixels,
		globalCompositeOperation: 'source-over',
		drawImage(source) {
			if (this.globalCompositeOperation === 'source-over') {
				if (pixels.length === 0) {
					pixels.push(...source.pixels.map((pixel) => [...pixel]));
					return;
				}
			}

			for (let index = 0; index < pixels.length; index += 1) {
				const destination = pixels[index];
				const overlay = source.pixels[index];
				const alpha = overlay[3] / 255;
				pixels[index] = [0, 1, 2].map((channel) =>
					Math.round(destination[channel] * (1 - alpha) + (
						this.globalCompositeOperation === 'multiply'
							? destination[channel] * overlay[channel] / 255
							: overlay[channel]
					) * alpha)
				).concat(255);
			}
		}
	};
}

test('approved composite preserves fills under white and transparent line-art pixels', () => {
	const fills = makePixelCanvas([
		[30, 41, 59, 255], // opaque white paper in the line-art image
		[30, 41, 59, 255], // transparent line-art pixel
		[30, 41, 59, 255], // antialiased black outline
	]);
	const lineArt = makePixelCanvas([
		[255, 255, 255, 255],
		[0, 0, 0, 0],
		[0, 0, 0, 128],
	]);
	const composite = makeCompositeContext();

	drawColourComposite(composite, fills, lineArt);

	assert.deepEqual(composite.pixels, [
		[30, 41, 59, 255],
		[30, 41, 59, 255],
		[15, 20, 29, 255],
	]);
});
