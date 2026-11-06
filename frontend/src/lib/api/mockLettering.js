import { previewOverride, panelFor } from '../lettering/geometry.js';
import metrics from '../lettering/fonts/mock-metrics.json' with { type: 'json' };

// The offline adapter uses recorded engine geometry. Placement stays exact;
// changing text uses simple Latin metrics for demonstration, not publication.
// Production always replaces its layer with the deterministic service result.
export function mockLettering(base, overrides, texts = {}) {
	let state = structuredClone(base);
	state.overrides = {};
	for (const [id, text] of Object.entries(texts)) {
		const panel = panelFor(state, id);
		if (!panel) continue;
		panel.utterances.find((u) => u.utterance_id === id).text = text;
		const bubble = state.layout.bubbles.find((b) => b.utterance_id === id);
		if (!bubble) continue;
		const line = bubble.lines[0], glyphs = [], b = bubble.shape.bounds;
		const unit = line.font_size / metrics.units_per_em;
		const ids = [...text].map((char) => metrics.cmap[char.codePointAt(0)]);
		bubble.flags = ids.includes(undefined) ? ['unsupported_glyph'] : [];
		if (bubble.flags.length) { bubble.lines = []; continue; }
		const total = ids.reduce((sum, id) => sum + metrics.advances[id] * unit / state.layout.page_aspect, 0);
		let x = b.x + (b.width - total) / 2;
		const baseline = { x, y: b.y + b.height / 2 + line.font_size * 0.3 };
		ids.forEach((id, cluster) => {
			const advance_x = metrics.advances[id] * unit / state.layout.page_aspect;
			glyphs.push({ glyph_id: id, x, y: baseline.y, advance_x, advance_y: 0, cluster });
			x += advance_x;
		});
		bubble.lines = [{ ...line, text, glyphs, baseline }];
		if (total > b.width * 0.8) bubble.flags.push('overflow');
	}
	for (const [id, override] of Object.entries(overrides)) state = previewOverride(state, id, override);
	state.layout.issues = [];
	for (const bubble of state.layout.bubbles) {
		const b = bubble.shape.bounds, p = panelFor(state, bubble.utterance_id).rect;
		const flags = new Set(bubble.flags);
		if (b.x < p.x || b.y < p.y || b.x + b.width > p.x + p.width || b.y + b.height > p.y + p.height) flags.add('out_of_panel');
		for (const other of state.layout.bubbles) {
			if (other === bubble) continue;
			const r = other.shape.bounds;
			if (b.x < r.x + r.width && b.x + b.width > r.x && b.y < r.y + r.height && b.y + b.height > r.y) flags.add('collision');
		}
		bubble.flags = [...flags];
		state.layout.issues.push(...bubble.flags.map((flag) => ({ flag, panel_id: bubble.panel_id, utterance_id: bubble.utterance_id })));
	}
	return state;
}
