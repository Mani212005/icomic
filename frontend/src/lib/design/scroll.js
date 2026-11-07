// Scroll effects. Everything here animates transform and opacity only, adds no
// dependency, and does nothing under prefers-reduced-motion.
const REVEAL_SELECTOR = '.character-card, .chapter-card, .panel-card, .page-preview-card, .option-card, .page-card, .compliance-notice';

function reducedMotion() {
	return typeof matchMedia !== 'function' || matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// Fade and lift cards the first time they scroll into view. Cards already on screen
// (or rendered later by a stage change near the top) are left untouched.
export function revealCards(node, selector = REVEAL_SELECTOR) {
	if (reducedMotion() || typeof IntersectionObserver !== 'function') return {};
	const seen = new WeakSet();
	const observer = new IntersectionObserver((entries) => {
		for (const entry of entries) {
			if (!entry.isIntersecting) continue;
			entry.target.dataset.reveal = 'shown';
			observer.unobserve(entry.target);
		}
	}, { rootMargin: '0px 0px -8% 0px' });
	function scan() {
		for (const element of node.querySelectorAll(selector)) {
			if (seen.has(element)) continue;
			seen.add(element);
			if (element.getBoundingClientRect().top < window.innerHeight) continue;
			element.dataset.reveal = 'hidden';
			observer.observe(element);
		}
	}
	scan();
	const mutations = new MutationObserver(scan);
	mutations.observe(node, { childList: true, subtree: true });
	return {
		destroy() {
			observer.disconnect();
			mutations.disconnect();
		}
	};
}

// Drift a decorative layer at a fraction of the scroll distance, capped at `max` px.
export function parallax(node, { factor = 0.12, max = 24 } = {}) {
	if (reducedMotion()) return {};
	let frame = 0;
	function paint() {
		frame = 0;
		node.style.transform = `translate3d(0, ${-Math.min(window.scrollY * factor, max).toFixed(1)}px, 0)`;
	}
	function onScroll() {
		if (!frame) frame = requestAnimationFrame(paint);
	}
	paint();
	window.addEventListener('scroll', onScroll, { passive: true });
	return {
		destroy() {
			window.removeEventListener('scroll', onScroll);
			if (frame) cancelAnimationFrame(frame);
		}
	};
}
