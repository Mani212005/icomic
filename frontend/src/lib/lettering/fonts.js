import comic from './fonts/comic-neue-v1.json' with { type: 'json' };
export { comic };
export const needsDevanagari = (layout) => layout.bubbles.some((bubble) => bubble.lines.some((line) => line.font_id === 'noto-devanagari-v1'));
export const loadDevanagari = () => import('./fonts/noto-devanagari-v1.json').then((module) => module.default);
