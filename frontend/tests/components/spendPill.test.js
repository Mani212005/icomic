import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import { tick } from 'svelte';
import SpendPill from '../../src/lib/design/SpendPill.svelte';
import SpendPillHarness from './SpendPillHarness.svelte';
import ComicEditor from '../../src/lib/editor/ComicEditor.svelte';
import { publishSpend } from '../../src/lib/api/spend.js';
import { STAGES } from '../../src/lib/api/types.js';

const spend = (cap, spent, left) => ({ enabled: true, cap_usd: cap, spent_usd: spent, remaining_usd: left });

beforeEach(() => { localStorage.clear(); publishSpend(null); });
afterEach(cleanup);

it('renders nothing while no cap is configured', () => {
	const view = render(SpendPillHarness);
	expect(view.queryByTestId('spend-pill')).toBeNull();
	expect(render(SpendPill, { view: null }).queryByTestId('spend-pill')).toBeNull();
});

it('shows the running total against the cap with the remainder available to assistive tech', async () => {
	const view = render(SpendPillHarness);
	publishSpend({ provider_spend: spend(2, 0.37, 1.63) });
	await tick();
	const pill = view.getByTestId('spend-pill');
	expect(pill.textContent).toContain('$0.37 / $2.00');
	expect(pill.getAttribute('title')).toBe('Spent $0.37 of the $2.00 cap. $1.63 left.');
	expect(view.getByText('Spent $0.37 of the $2.00 cap. $1.63 left.')).toBeTruthy();
	expect(pill.className).toContain('ok');
});

it('follows the spend as it changes, then marks the pill when the cap is nearly and fully used', async () => {
	const view = render(SpendPillHarness);
	publishSpend({ provider_spend: spend(2, 0.01, 1.99) });
	await tick();
	expect(view.getByTestId('spend-pill').textContent).toContain('$0.01 / $2.00');
	publishSpend({ provider_spend: spend(2, 1.7, 0.3) });
	await tick();
	expect(view.getByTestId('spend-pill').className).toContain('low');
	publishSpend({ provider_spend: spend(2, 2, 0) });
	await tick();
	expect(view.getByTestId('spend-pill').className).toContain('out');
	expect(view.getByTestId('spend-pill').getAttribute('title')).toContain('Spend cap reached');
	publishSpend({ balance: 3 });
	await tick();
	expect(view.queryByTestId('spend-pill')).toBeNull();
});

it('the editor publishes the spend it reads with credit usage, so the header follows each paid call', async () => {
	const view = render(SpendPillHarness);
	let usage = { remaining_credits: 5, provider_spend: spend(2, 0.1, 1.9) };
	const apiClient = {
		getCharacters: async () => [],
		getChapters: async () => [],
		getCreditUsage: vi.fn(async () => usage),
		estimateCost: async () => ({ estimated_cost: 0.01, currency: 'USD', price_verified: true })
	};
	const editor = render(ComicEditor, { project: { id: 'p1', title: 'Tide', original_script: 'A hero.', chapter_count: 1 }, apiClient, activeStage: STAGES.CHARACTERS });
	await vi.waitFor(() => expect(view.getByTestId('spend-pill').textContent).toContain('$0.10 / $2.00'));
	usage = { remaining_credits: 4.99, provider_spend: spend(2, 0.11, 1.89) };
	await editor.component.reloadProjectData();
	await vi.waitFor(() => expect(view.getByTestId('spend-pill').textContent).toContain('$0.11 / $2.00'));
});
