import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import Harness from './SidebarHarness.svelte';

function mockPhone(matches) {
	window.matchMedia = vi.fn().mockImplementation(() => ({
		matches, addEventListener: vi.fn(), removeEventListener: vi.fn()
	}));
}
const panelOf = (view) => view.container.querySelector('#side');
const edgeOf = (view) => view.getByTestId('sidebar-edge');
const burger = (view) => view.getByRole('button', { name: 'Menu' });
const isOpen = (view) => burger(view).getAttribute('aria-expanded') === 'true';

beforeEach(() => mockPhone(false));
afterEach(() => {
	cleanup();
	vi.useRealTimers();
});

describe('hamburger', () => {
	it('is a labelled button wired to the panel', async () => {
		const view = render(Harness);
		expect(burger(view).getAttribute('aria-controls')).toBe('side');
		expect(burger(view).getAttribute('aria-expanded')).toBe('false');
		expect(panelOf(view).hasAttribute('inert')).toBe(true);
		await fireEvent.click(burger(view));
		expect(isOpen(view)).toBe(true);
		expect(panelOf(view).hasAttribute('inert')).toBe(false);
	});

	it('moves focus into the panel on open and back to the button on close', async () => {
		const view = render(Harness);
		await fireEvent.click(burger(view));
		await vi.waitFor(() => expect(panelOf(view).contains(document.activeElement)).toBe(true));
		await fireEvent.click(burger(view));
		expect(isOpen(view)).toBe(false);
		expect(document.activeElement).toBe(burger(view));
	});

	it('closes on Escape and returns focus to the button', async () => {
		const view = render(Harness);
		await fireEvent.click(burger(view));
		await fireEvent.keyDown(document.activeElement, { key: 'Escape' });
		expect(isOpen(view)).toBe(false);
		expect(document.activeElement).toBe(burger(view));
	});

	it('closes on an outside press but not on a press inside', async () => {
		const view = render(Harness);
		await fireEvent.click(burger(view));
		await fireEvent.pointerDown(view.getByRole('button', { name: 'One' }));
		expect(isOpen(view)).toBe(true);
		await fireEvent.pointerDown(view.getByRole('button', { name: 'Outside' }));
		expect(isOpen(view)).toBe(false);
	});
});

describe('cursor behaviour', () => {
	it('opens after a short dwell on the left edge without taking focus', async () => {
		vi.useFakeTimers();
		const view = render(Harness);
		view.getByRole('button', { name: 'Before' }).focus();
		await fireEvent.pointerEnter(edgeOf(view));
		expect(isOpen(view)).toBe(false);
		await vi.advanceTimersByTimeAsync(150);
		expect(isOpen(view)).toBe(true);
		expect(document.activeElement).toBe(view.getByRole('button', { name: 'Before' }));
	});

	it('ignores a pointer that only brushes the edge', async () => {
		vi.useFakeTimers();
		const view = render(Harness);
		await fireEvent.pointerEnter(edgeOf(view));
		await fireEvent.pointerLeave(edgeOf(view));
		await vi.advanceTimersByTimeAsync(300);
		expect(isOpen(view)).toBe(false);
	});

	it('ignores touch pointers on the edge', async () => {
		vi.useFakeTimers();
		const view = render(Harness);
		await fireEvent.pointerEnter(edgeOf(view), { pointerType: 'touch' });
		await vi.advanceTimersByTimeAsync(300);
		expect(isOpen(view)).toBe(false);
	});

	it('closes shortly after the pointer leaves, and stays open if it comes back', async () => {
		vi.useFakeTimers();
		const view = render(Harness);
		await fireEvent.click(burger(view));
		await fireEvent.pointerEnter(panelOf(view));
		await fireEvent.pointerLeave(panelOf(view));
		await vi.advanceTimersByTimeAsync(100);
		await fireEvent.pointerEnter(panelOf(view));
		await vi.advanceTimersByTimeAsync(400);
		expect(isOpen(view)).toBe(true);
		await fireEvent.pointerLeave(panelOf(view));
		await vi.advanceTimersByTimeAsync(400);
		expect(isOpen(view)).toBe(false);
	});

	it('never closes while the pointer is inside', async () => {
		vi.useFakeTimers();
		const view = render(Harness);
		await fireEvent.click(burger(view));
		await fireEvent.pointerEnter(panelOf(view));
		await vi.advanceTimersByTimeAsync(2000);
		expect(isOpen(view)).toBe(true);
	});

	it('does not close while a field has focus, and closes once it blurs', async () => {
		vi.useFakeTimers();
		const view = render(Harness);
		await fireEvent.click(burger(view));
		await fireEvent.pointerEnter(panelOf(view));
		const field = view.getByLabelText('Name');
		field.focus();
		await fireEvent.pointerLeave(panelOf(view));
		await vi.advanceTimersByTimeAsync(600);
		expect(isOpen(view)).toBe(true);
		field.blur();
		await vi.advanceTimersByTimeAsync(50);
		expect(isOpen(view)).toBe(false);
	});

	it('does not open from the edge while disabled, and closes if disabled while open', async () => {
		vi.useFakeTimers();
		const view = render(Harness, { disabled: true });
		await fireEvent.pointerEnter(edgeOf(view));
		await vi.advanceTimersByTimeAsync(300);
		expect(isOpen(view)).toBe(false);
		await view.rerender({ disabled: false });
		await fireEvent.click(burger(view));
		expect(isOpen(view)).toBe(true);
		await view.rerender({ disabled: true });
		expect(isOpen(view)).toBe(false);
	});
});

describe('phone drawer', () => {
	it('acts as a modal dialog and closes after choosing an item', async () => {
		mockPhone(true);
		const view = render(Harness);
		await fireEvent.click(burger(view));
		expect(panelOf(view).getAttribute('role')).toBe('dialog');
		expect(panelOf(view).getAttribute('aria-modal')).toBe('true');
		await fireEvent.click(view.getByRole('button', { name: 'Two' }));
		expect(isOpen(view)).toBe(false);
	});

	it('keeps Tab inside the hamburger and the panel', async () => {
		mockPhone(true);
		const view = render(Harness);
		await fireEvent.click(burger(view));
		view.getByRole('button', { name: 'Two' }).focus();
		await fireEvent.keyDown(document.activeElement, { key: 'Tab' });
		expect(document.activeElement).toBe(burger(view));
		await fireEvent.keyDown(document.activeElement, { key: 'Tab', shiftKey: true });
		expect(document.activeElement).toBe(view.getByRole('button', { name: 'Two' }));
	});
});
