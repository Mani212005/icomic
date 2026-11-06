export const HEIGHT = 1000;
export const pageWidth = (layout) => layout.page_aspect * HEIGHT;
export const panelFor = (state, id) => state.input.panels.find((panel) => panel.utterances.some((u) => u.utterance_id === id));
export const utterances = (state) => state?.input.panels.flatMap((panel) => panel.utterances) || [];
export const localPoint = (point, panel) => ({ x: (point.x - panel.rect.x) / panel.rect.width, y: (point.y - panel.rect.y) / panel.rect.height });
export const pagePoint = (point, panel) => ({ x: panel.rect.x + point.x * panel.rect.width, y: panel.rect.y + point.y * panel.rect.height });
export const centerOf = (bubble) => ({ x: bubble.shape.bounds.x + bubble.shape.bounds.width / 2, y: bubble.shape.bounds.y + bubble.shape.bounds.height / 2 });
export const tipOf = (bubble) => bubble.tail[Math.floor(bubble.tail.length / 2)] || (bubble.thought_tail.at(-1) && centerOf({ shape: bubble.thought_tail.at(-1) }));

// SVG uses physical page-height units, so circles, radii, strokes and glyphs
// retain their shape on a nonsquare page. Result X and Y remain page-normalized.
export function shapePath(shape, width) {
	const b = shape.bounds;
	const x = b.x * width, y = b.y * HEIGHT, w = b.width * width, h = b.height * HEIGHT;
	if (shape.kind === 'ellipse') return `M${x} ${y + h / 2}a${w / 2} ${h / 2} 0 1 0 ${w} 0a${w / 2} ${h / 2} 0 1 0 ${-w} 0Z`;
	const r = Math.min(shape.radius * HEIGHT, w / 2, h / 2);
	return `M${x + r} ${y}h${w - 2 * r}a${r} ${r} 0 0 1 ${r} ${r}v${h - 2 * r}a${r} ${r} 0 0 1 ${-r} ${r}h${2 * r - w}a${r} ${r} 0 0 1 ${-r} ${-r}v${2 * r - h}a${r} ${r} 0 0 1 ${r} ${-r}Z`;
}

export function bubblePath(bubble, width) {
	if (!bubble.tail.length || bubble.shape.kind !== 'ellipse') return shapePath(bubble.shape, width);
	const b = bubble.shape.bounds, a = bubble.tail[0], z = bubble.tail.at(-1);
	const c = centerOf(bubble), rx = b.width / 2, ry = b.height / 2;
	const start = Math.atan2((a.y - c.y) / ry, (a.x - c.x) / rx);
	const end = Math.atan2((z.y - c.y) / ry, (z.x - c.x) / rx);
	const delta = (end - start + 2 * Math.PI) % (2 * Math.PI);
	// Replace the short body arc with the serialized tail boundary. One closed
	// outline gives an open tail seam without a filter or approximate polygon.
	return `M${bubble.tail.map((p) => `${p.x * width} ${p.y * HEIGHT}`).join('L')}A${rx * width} ${ry * HEIGHT} 0 1 ${delta < Math.PI ? 1 : 0} ${a.x * width} ${a.y * HEIGHT}Z`;
}

export function movedOverride(state, id, delta, tail = false) {
	const bubble = state.layout.bubbles.find((b) => b.utterance_id === id);
	const panel = panelFor(state, id);
	const old = state.overrides[id] || {};
	if (tail) {
		const point = old.tail_anchor || localPoint(tipOf(bubble) || centerOf(bubble), panel);
		return { ...old, tail_anchor: { x: point.x + delta.x / panel.rect.width, y: point.y + delta.y / panel.rect.height } };
	}
	if (old.rect) return { ...old, rect: { ...old.rect, x: old.rect.x + delta.x / panel.rect.width, y: old.rect.y + delta.y / panel.rect.height } };
	const point = old.center || localPoint(centerOf(bubble), panel);
	return { ...old, center: { x: point.x + delta.x / panel.rect.width, y: point.y + delta.y / panel.rect.height } };
}

export function nudge(event) {
	const step = event.shiftKey ? 0.01 : 0.001;
	return { ArrowLeft: { x: -step, y: 0 }, ArrowRight: { x: step, y: 0 }, ArrowUp: { x: 0, y: -step }, ArrowDown: { x: 0, y: step } }[event.key];
}

// Only a provisional visual while a request is in flight. The returned server
// layout always replaces this, including all flags and automatic neighbours.
export function previewOverride(state, id, override) {
	const next = structuredClone(state);
	const bubble = next.layout.bubbles.find((b) => b.utterance_id === id);
	next.overrides[id] = override;
	if (!bubble || !Object.keys(override).length) return next;
	const panel = panelFor(state, id), old = state.overrides[id] || {};
	const before = centerOf(bubble);
	const after = override.rect ? pagePoint({ x: override.rect.x + override.rect.width / 2, y: override.rect.y + override.rect.height / 2 }, panel) : override.center ? pagePoint(override.center, panel) : before;
	const scale = (override.size_scale || 1) / (old.size_scale || 1);
	const point = (p) => ({ ...p, x: after.x + (p.x - before.x) * scale, y: after.y + (p.y - before.y) * scale });
	const bodyScale = override.rect ? 1 : scale;
	const bodyPoint = (p) => ({ ...p, x: after.x + (p.x - before.x) * bodyScale, y: after.y + (p.y - before.y) * bodyScale });
	const shape = (s) => ({ ...s, radius: s.radius * bodyScale, bounds: { ...bodyPoint(s.bounds), width: s.bounds.width * bodyScale, height: s.bounds.height * bodyScale } });
	bubble.shape = shape(bubble.shape);
	bubble.tail = bubble.tail.map(bodyPoint);
	bubble.thought_tail = bubble.thought_tail.map(shape);
	bubble.lines = bubble.lines.map((line) => ({ ...line, font_size: line.font_size * scale, baseline: point(line.baseline), glyphs: line.glyphs.map(point) }));
	if (override.tail_anchor && bubble.tail.length) {
		const tip = pagePoint(override.tail_anchor, panel), middle = Math.floor(bubble.tail.length / 2), previous = bubble.tail[middle];
		bubble.tail = bubble.tail.map((p, i) => ({ x: p.x + (tip.x - previous.x) * (1 - Math.abs(i - middle) / middle), y: p.y + (tip.y - previous.y) * (1 - Math.abs(i - middle) / middle) }));
	}
	return next;
}
