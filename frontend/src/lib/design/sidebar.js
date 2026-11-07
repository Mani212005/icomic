// Behaviour helpers for Sidebar.svelte. Kept in plain JS so the component script stays small.
export const SOURCE = Object.freeze({
	BUTTON: 'button',
	EDGE: 'edge',
	ESCAPE: 'escape',
	OUTSIDE: 'outside',
	LEAVE: 'leave',
	SELECT: 'select',
	DISABLED: 'disabled',
	API: 'api'
});

export const EDGE_OPEN_DELAY = 120;
export const LEAVE_CLOSE_DELAY = 280;

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';
const FIELD = 'input, select, textarea, [contenteditable="true"]';
const ACTIVATOR = 'button, a[href]';

export const isTouch = (event) => event.pointerType === 'touch';
export const isField = (element) => !!element?.closest?.(FIELD);
export const isActivator = (target) => !!target?.closest?.(ACTIVATOR);
export const isEscape = (event) => event.key === 'Escape';
export const isTab = (event) => event.key === 'Tab';

export function focusFirst(panel) {
	(panel?.querySelector(`[aria-current], ${FOCUSABLE}`) || panel)?.focus();
}

// Phone drawers behave like dialogs: Tab cycles through the hamburger and the panel only.
export function trapTab(event, panel, trigger) {
	const items = [trigger, ...panel.querySelectorAll(FOCUSABLE)].filter(Boolean);
	if (!items.length) return;
	const first = items[0];
	const last = items[items.length - 1];
	const current = document.activeElement;
	if (event.shiftKey && (current === first || current === panel)) {
		event.preventDefault();
		last.focus();
	} else if (!event.shiftKey && current === last) {
		event.preventDefault();
		first.focus();
	}
}

export function watchPhone(onChange) {
	const query = window.matchMedia('(max-width: 700px)');
	const sync = () => onChange(query.matches);
	sync();
	query.addEventListener('change', sync);
	return () => query.removeEventListener('change', sync);
}

export function watchOutside(onPointerDown) {
	document.addEventListener('pointerdown', onPointerDown, true);
	return () => document.removeEventListener('pointerdown', onPointerDown, true);
}
