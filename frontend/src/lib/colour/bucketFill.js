/**
 * Client-side flood-fill algorithm for paint-bucket tool.
 * Evaluates darkness threshold on black-and-white line art.
 * Operates purely client-side without network or AI API calls.
 */

export function hexToRgb(hex) {
	const cleaned = hex.replace('#', '');
	const num = parseInt(cleaned, 16);
	return {
		r: (num >> 16) & 255,
		g: (num >> 8) & 255,
		b: num & 255
	};
}

export function isBoundaryPixel(r, g, b, darknessThreshold = 60) {
	// Treat pixels darker than the threshold as black outline boundary
	const luminance = 0.299 * r + 0.587 * g + 0.114 * b;
	return luminance < darknessThreshold;
}

export function floodFill(imageData, startX, startY, fillHex, darknessThreshold = 60) {
	const { width, height, data } = imageData;
	if (startX < 0 || startX >= width || startY < 0 || startY >= height) {
		return imageData;
	}

	const fill = hexToRgb(fillHex);
	const startIdx = (startY * width + startX) * 4;
	const startR = data[startIdx];
	const startG = data[startIdx + 1];
	const startB = data[startIdx + 2];

	if (isBoundaryPixel(startR, startG, startB, darknessThreshold)) {
		return imageData; // Don't fill over line art boundaries
	}

	const visited = new Uint8Array(width * height);
	const queue = [[startX, startY]];

	while (queue.length > 0) {
		const [x, y] = queue.pop();
		const idx = y * width + x;
		if (visited[idx]) continue;
		visited[idx] = 1;

		const pIdx = idx * 4;
		const r = data[pIdx];
		const g = data[pIdx + 1];
		const b = data[pIdx + 2];

		if (isBoundaryPixel(r, g, b, darknessThreshold)) {
			continue;
		}

		// Apply fill colour
		data[pIdx] = fill.r;
		data[pIdx + 1] = fill.g;
		data[pIdx + 2] = fill.b;
		data[pIdx + 3] = 255;

		if (x > 0 && !visited[idx - 1]) queue.push([x - 1, y]);
		if (x < width - 1 && !visited[idx + 1]) queue.push([x + 1, y]);
		if (y > 0 && !visited[idx - width]) queue.push([x, y - 1]);
		if (y < height - 1 && !visited[idx + width]) queue.push([x, y + 1]);
	}

	return imageData;
}
