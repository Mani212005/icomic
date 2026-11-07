import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { revealCards, parallax } from '../../src/lib/design/scroll.js';

let observers;
function mockMotion(reduce) {
	window.matchMedia = vi.fn().mockImplementation(() => ({ matches: reduce }));
	observers = [];
	window.IntersectionObserver = class {
		constructor(callback) {
			this.callback = callback;
			this.watched = new Set();
			observers.push(this);
		}
		observe(element) { this.watched.add(element); }
		unobserve(element) { this.watched.delete(element); }
		disconnect() { this.watched.clear(); }
	};
}
function card(top) {
	const element = document.createElement('div');
	element.className = 'character-card';
	element.getBoundingClientRect = () => ({ top });
	return element;
}

beforeEach(() => {
	document.body.innerHTML = '';
});
afterEach(() => vi.restoreAllMocks());

describe('revealCards', () => {
	it('hides only cards below the fold and shows them once they intersect', () => {
		mockMotion(false);
		const root = document.createElement('div');
		const onScreen = card(100);
		const below = card(window.innerHeight + 400);
		root.append(onScreen, below);
		document.body.append(root);
		revealCards(root);
		expect(onScreen.dataset.reveal).toBeUndefined();
		expect(below.dataset.reveal).toBe('hidden');
		observers[0].callback([{ target: below, isIntersecting: true }]);
		expect(below.dataset.reveal).toBe('shown');
		expect(observers[0].watched.has(below)).toBe(false);
	});

	it('does nothing at all under prefers-reduced-motion', () => {
		mockMotion(true);
		const root = document.createElement('div');
		const below = card(window.innerHeight + 400);
		root.append(below);
		document.body.append(root);
		revealCards(root);
		expect(observers).toHaveLength(0);
		expect(below.dataset.reveal).toBeUndefined();
	});
});

describe('parallax', () => {
	it('moves the layer with transform only, capped', async () => {
		mockMotion(false);
		vi.stubGlobal('requestAnimationFrame', (callback) => { callback(); return 1; });
		const layer = document.createElement('div');
		const action = parallax(layer, { factor: 0.5, max: 10 });
		Object.defineProperty(window, 'scrollY', { value: 100, configurable: true });
		window.dispatchEvent(new Event('scroll'));
		expect(layer.style.transform).toBe('translate3d(0, -10px, 0)');
		expect(layer.style.top).toBe('');
		action.destroy();
		vi.unstubAllGlobals();
	});

	it('does nothing under prefers-reduced-motion', () => {
		mockMotion(true);
		const layer = document.createElement('div');
		parallax(layer);
		expect(layer.style.transform).toBe('');
	});
});
