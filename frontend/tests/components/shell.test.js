import { afterEach, expect, it, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import AppFooter from '../../src/lib/design/AppFooter.svelte';
import AppHeader from '../../src/lib/design/AppHeader.svelte';
import StageNav from '../../src/lib/design/StageNav.svelte';
import { stageList } from '../../src/lib/editor/stages.js';

afterEach(cleanup);
vi.stubGlobal('matchMedia', () => ({ matches: true }));

it('footer shows the mark, Beta, real links and the current year', () => {
	const view = render(AppFooter);
	expect(view.getByAltText('Icomic').getAttribute('src')).toBe('/brand/wordmark.svg');
	expect(view.getByText('Beta')).toBeTruthy();
	expect(view.getByRole('link', { name: 'Docs' }).getAttribute('href')).toContain('/docs');
	expect(view.getByRole('link', { name: 'README' }).getAttribute('href')).toContain('#readme');
	expect(view.container.querySelector('.copyright').textContent).toContain(String(new Date().getFullYear()));
});

it('header carries the logo, the Beta tag and a slot for an account control', () => {
	const view = render(AppHeader);
	expect(view.getByAltText('Icomic')).toBeTruthy();
	expect(view.getByText('Beta')).toBeTruthy();
	expect(view.container.querySelector('.account')).toBeTruthy();
});

it('stage navigation marks the current and completed stages', () => {
	const view = render(StageNav, { stages: stageList, active: 'characters', completed: { script: true } });
	expect(view.getByRole('button', { name: /Characters/ }).getAttribute('aria-current')).toBe('step');
	expect(view.getByRole('button', { name: /Script/ }).querySelector('.completion-mark')).toBeTruthy();
	expect(view.getAllByRole('button')).toHaveLength(stageList.length);
});
