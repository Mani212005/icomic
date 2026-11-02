/**
 * Client-side flood-fill algorithm and canvas history for the paint-bucket tool.
 *
 * SPEC REQUIREMENTS:
 * - Visited-on-enqueue flood fill preventing duplicate queue entries and stack explosion.
 * - Handles large canvases without memory blowup (flat typed arrays, zero object allocation per pixel).
 * - Fills by tolerance against the clicked pixel colour.
 * - Treats only the line art's actual boundary as a barrier (does NOT treat dark colours
 *   painted on the colour layer as barriers).
 * - Morphological gap-closing to prevent leakage through small line art gaps.
 * - Client-side undo history (configurable N, default 20).
 *
 * References:
 * - docs/SPEC.md: Section 3.8
 * - docs/ARCHITECTURE.md: Section 9.3, 9.4
 */

/**
 * Parse hex color into RGBA components.
 * Supports #RGB, #RRGGBB, #RRGGBBAA.
 * @param {string} hex
 * @returns {{ r: number, g: number, b: number, a: number }}
 */
export function hexToRgba(hex) {
	if (!hex) return { r: 0, g: 0, b: 0, a: 255 };
	let cleaned = hex.trim().replace(/^#/, '');

	if (cleaned.length === 3) {
		cleaned = cleaned
			.split('')
			.map((c) => c + c)
			.join('') + 'ff';
	} else if (cleaned.length === 6) {
		cleaned += 'ff';
	}

	const num = parseInt(cleaned, 16);
	if (isNaN(num)) {
		return { r: 0, g: 0, b: 0, a: 255 };
	}

	return {
		r: (num >>> 24) & 255,
		g: (num >>> 16) & 255,
		b: (num >>> 8) & 255,
		a: num & 255
	};
}

/**
 * Calculate standard relative luminance of an RGB pixel.
 * @param {number} r
 * @param {number} g
 * @param {number} b
 * @returns {number}
 */
export function getLuminance(r, g, b) {
	return 0.299 * r + 0.587 * g + 0.114 * b;
}

/**
 * Legacy hexToRgb helper for backwards compatibility.
 * @param {string} hex
 * @returns {{ r: number, g: number, b: number }}
 */
export function hexToRgb(hex) {
	const { r, g, b } = hexToRgba(hex);
	return { r, g, b };
}

/**
 * Determine if a pixel in the line art layer is an ink outline boundary.
 * Anti-aliased pixels near black (luminance < darknessThreshold) and opaque are boundaries.
 * Transparent pixels (alpha < 30) are not line art boundaries.
 * @param {number} r
 * @param {number} g
 * @param {number} b
 * @param {number} a
 * @param {number} [darknessThreshold=60]
 * @returns {boolean}
 */
export function isLineArtBoundary(r, g, b, a, darknessThreshold = 60) {
	if (a < 30) {
		return false; // Transparent pixel is not line art
	}
	return getLuminance(r, g, b) < darknessThreshold;
}

/**
 * Legacy isBoundaryPixel helper for backwards compatibility.
 */
export function isBoundaryPixel(r, g, b, darknessThreshold = 60) {
	return isLineArtBoundary(r, g, b, 255, darknessThreshold);
}

/**
 * Color distance check squared between two RGBA colors.
 */
function isColorWithinTolerance(r1, g1, b1, a1, r2, g2, b2, a2, toleranceSq) {
	const dr = r1 - r2;
	const dg = g1 - g2;
	const db = b1 - b2;
	const da = a1 - a2;
	return dr * dr + dg * dg + db * db + da * da <= toleranceSq;
}

/**
 * Create a binary morphological boundary mask for gap closing.
 * Dilates line art ink boundaries by the specified radius to bridge small contour gaps.
 * @param {{ width: number, height: number, data: Uint8Array|Uint8ClampedArray }} lineArtData
 * @param {number} [radius=1]
 * @param {number} [darknessThreshold=60]
 * @returns {Uint8Array} Binary mask where 1 indicates barrier.
 */
export function createGapClosedBoundaryMask(lineArtData, radius = 1, darknessThreshold = 60) {
	const { width, height, data } = lineArtData;
	const totalPixels = width * height;
	const rawMask = new Uint8Array(totalPixels);

	for (let i = 0; i < totalPixels; i++) {
		const p = i * 4;
		if (isLineArtBoundary(data[p], data[p + 1], data[p + 2], data[p + 3], darknessThreshold)) {
			rawMask[i] = 1;
		}
	}

	if (radius <= 0) {
		return rawMask;
	}

	const dilatedMask = new Uint8Array(totalPixels);
	for (let y = 0; y < height; y++) {
		for (let x = 0; x < width; x++) {
			const idx = y * width + x;
			if (rawMask[idx] === 1) {
				dilatedMask[idx] = 1;
				continue;
			}
			let hasNeighbor = false;
			for (let dy = -radius; dy <= radius && !hasNeighbor; dy++) {
				const ny = y + dy;
				if (ny < 0 || ny >= height) continue;
				for (let dx = -radius; dx <= radius; dx++) {
					const nx = x + dx;
					if (nx < 0 || nx >= width) continue;
					if (rawMask[ny * width + nx] === 1) {
						hasNeighbor = true;
						break;
					}
				}
			}
			if (hasNeighbor) {
				dilatedMask[idx] = 1;
			}
		}
	}

	return dilatedMask;
}

/**
 * High-performance, memory-bounded, visited-on-enqueue flood fill.
 *
 * @param {{ width: number, height: number, data: Uint8Array|Uint8ClampedArray }} imageData The canvas/colour layer being modified.
 * @param {number} startX Clicked X coordinate
 * @param {number} startY Clicked Y coordinate
 * @param {string|{ r: number, g: number, b: number, a?: number }} fillColor Fill colour (hex string or RGBA)
 * @param {Object} [options={}]
 * @param {{ width: number, height: number, data: Uint8Array|Uint8ClampedArray }} [options.lineArtData] Optional line art layer
 * @param {Uint8Array} [options.boundaryMask] Pre-computed boundary mask (e.g. from gap-closing)
 * @param {number} [options.tolerance=32] Colour matching tolerance against clicked pixel
 * @param {number} [options.darknessThreshold=60] Darkness threshold for line art ink lines
 * @param {number} [options.gapClosingRadius=0] Morphological gap-closing radius (0 = disabled)
 * @param {(x: number, y: number, flatIdx: number) => boolean} [options.isBoundary] Custom barrier predicate
 * @returns {{ width: number, height: number, data: Uint8Array|Uint8ClampedArray }}
 */
export function floodFill(imageData, startX, startY, fillColor, options = {}) {
	const { width, height, data } = imageData;

	startX = Math.floor(startX);
	startY = Math.floor(startY);

	if (startX < 0 || startX >= width || startY < 0 || startY >= height) {
		return imageData;
	}

	const resolvedOptions = typeof options === 'number' ? { darknessThreshold: options } : options;
	const {
		lineArtData = null,
		boundaryMask = null,
		tolerance = 32,
		darknessThreshold = 60,
		gapClosingRadius = 0,
		isBoundary = null
	} = resolvedOptions;

	const fill = typeof fillColor === 'string' ? hexToRgba(fillColor) : { a: 255, ...fillColor };
	const toleranceSq = tolerance * tolerance;

	const totalPixels = width * height;
	const startFlat = startY * width + startX;
	const startP = startFlat * 4;

	const targetR = data[startP];
	const targetG = data[startP + 1];
	const targetB = data[startP + 2];
	const targetA = data[startP + 3];

	// Prepare barrier lookup
	let activeBoundaryMask = boundaryMask;
	if (!activeBoundaryMask && lineArtData && gapClosingRadius > 0) {
		activeBoundaryMask = createGapClosedBoundaryMask(lineArtData, gapClosingRadius, darknessThreshold);
	}

	const checkIsBarrier = (x, y, flatIdx) => {
		if (isBoundary) {
			return isBoundary(x, y, flatIdx);
		}
		if (activeBoundaryMask) {
			return activeBoundaryMask[flatIdx] === 1;
		}
		if (lineArtData) {
			const lp = flatIdx * 4;
			const ld = lineArtData.data;
			return isLineArtBoundary(ld[lp], ld[lp + 1], ld[lp + 2], ld[lp + 3], darknessThreshold);
		}
		// If lineArtData is not provided, evaluate boundary on imageData only if
		// it is an ink line that differs from the target clicked color.
		const lp = flatIdx * 4;
		const r = data[lp];
		const g = data[lp + 1];
		const b = data[lp + 2];
		const a = data[lp + 3];
		const isInk = isLineArtBoundary(r, g, b, a, darknessThreshold);
		const matchesTarget = isColorWithinTolerance(r, g, b, a, targetR, targetG, targetB, targetA, toleranceSq);
		return isInk && !matchesTarget;
	};

	// If clicked pixel is itself a line art boundary, do not fill
	if (checkIsBarrier(startX, startY, startFlat)) {
		return imageData;
	}

	// If target colour is already identical to fill colour within tolerance, no-op
	if (isColorWithinTolerance(targetR, targetG, targetB, targetA, fill.r, fill.g, fill.b, fill.a, toleranceSq)) {
		return imageData;
	}

	// Visited tracking: 1 byte per pixel. Marked VISITED ON ENQUEUE.
	const visited = new Uint8Array(totalPixels);

	// Flat Int32Array stack. Starts compact and expands dynamically if required.
	// Zero per-pixel heap object allocations.
	let stackCapacity = Math.min(65536, totalPixels);
	let stack = new Int32Array(stackCapacity);
	let stackPtr = 0;

	const push = (flatIdx) => {
		if (stackPtr >= stackCapacity) {
			const newCapacity = Math.min(totalPixels, stackCapacity * 2);
			const newStack = new Int32Array(newCapacity);
			newStack.set(stack);
			stack = newStack;
			stackCapacity = newCapacity;
		}
		stack[stackPtr++] = flatIdx;
	};

	// Enqueue start pixel - mark visited ON ENQUEUE
	visited[startFlat] = 1;
	push(startFlat);

	while (stackPtr > 0) {
		const flatIdx = stack[--stackPtr];
		const x = flatIdx % width;
		const y = (flatIdx / width) | 0;

		const p = flatIdx * 4;
		data[p] = fill.r;
		data[p + 1] = fill.g;
		data[p + 2] = fill.b;
		data[p + 3] = fill.a;

		// 4-way neighbors
		// West (x - 1, y)
		if (x > 0) {
			const nFlat = flatIdx - 1;
			if (!visited[nFlat]) {
				if (checkIsBarrier(x - 1, y, nFlat)) {
					visited[nFlat] = 1;
				} else {
					const np = nFlat * 4;
					if (isColorWithinTolerance(data[np], data[np + 1], data[np + 2], data[np + 3], targetR, targetG, targetB, targetA, toleranceSq)) {
						visited[nFlat] = 1; // Mark visited ON ENQUEUE
						push(nFlat);
					}
				}
			}
		}

		// East (x + 1, y)
		if (x < width - 1) {
			const nFlat = flatIdx + 1;
			if (!visited[nFlat]) {
				if (checkIsBarrier(x + 1, y, nFlat)) {
					visited[nFlat] = 1;
				} else {
					const np = nFlat * 4;
					if (isColorWithinTolerance(data[np], data[np + 1], data[np + 2], data[np + 3], targetR, targetG, targetB, targetA, toleranceSq)) {
						visited[nFlat] = 1; // Mark visited ON ENQUEUE
						push(nFlat);
					}
				}
			}
		}

		// North (x, y - 1)
		if (y > 0) {
			const nFlat = flatIdx - width;
			if (!visited[nFlat]) {
				if (checkIsBarrier(x, y - 1, nFlat)) {
					visited[nFlat] = 1;
				} else {
					const np = nFlat * 4;
					if (isColorWithinTolerance(data[np], data[np + 1], data[np + 2], data[np + 3], targetR, targetG, targetB, targetA, toleranceSq)) {
						visited[nFlat] = 1; // Mark visited ON ENQUEUE
						push(nFlat);
					}
				}
			}
		}

		// South (x, y + 1)
		if (y < height - 1) {
			const nFlat = flatIdx + width;
			if (!visited[nFlat]) {
				if (checkIsBarrier(x, y + 1, nFlat)) {
					visited[nFlat] = 1;
				} else {
					const np = nFlat * 4;
					if (isColorWithinTolerance(data[np], data[np + 1], data[np + 2], data[np + 3], targetR, targetG, targetB, targetA, toleranceSq)) {
						visited[nFlat] = 1; // Mark visited ON ENQUEUE
						push(nFlat);
					}
				}
			}
		}
	}

	return imageData;
}

/**
 * Canvas History Manager for Colour Editor Undo / Redo.
 * Supports configurable maximum history (default: 20 actions).
 */
export class CanvasHistory {
	constructor(maxHistory = 20) {
		this.maxHistory = maxHistory;
		this.undoStack = [];
		this.redoStack = [];
	}

	/**
	 * Record a canvas state before an operation.
	 * @param {Uint8Array|Uint8ClampedArray} pixelData
	 */
	record(pixelData) {
		if (!pixelData) return;
		this.undoStack.push(new Uint8ClampedArray(pixelData));
		if (this.undoStack.length > this.maxHistory) {
			this.undoStack.shift();
		}
		this.redoStack = []; // Clear redo stack on new action
	}

	/**
	 * Undo the last action.
	 * @param {Uint8Array|Uint8ClampedArray} currentPixelData
	 * @returns {Uint8ClampedArray|null} Previous pixel data or null if cannot undo.
	 */
	undo(currentPixelData) {
		if (this.undoStack.length === 0) return null;
		if (currentPixelData) {
			this.redoStack.push(new Uint8ClampedArray(currentPixelData));
		}
		return this.undoStack.pop();
	}

	/**
	 * Redo the previously undone action.
	 * @param {Uint8Array|Uint8ClampedArray} currentPixelData
	 * @returns {Uint8ClampedArray|null} Redone pixel data or null if cannot redo.
	 */
	redo(currentPixelData) {
		if (this.redoStack.length === 0) return null;
		if (currentPixelData) {
			this.undoStack.push(new Uint8ClampedArray(currentPixelData));
		}
		return this.redoStack.pop();
	}

	get canUndo() {
		return this.undoStack.length > 0;
	}

	get canRedo() {
		return this.redoStack.length > 0;
	}

	get length() {
		return this.undoStack.length;
	}

	clear() {
		this.undoStack = [];
		this.redoStack = [];
	}
}
