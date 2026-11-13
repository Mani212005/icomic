import { writable } from 'svelte/store';

// Provider spend against the personal cap, as the credit-usage endpoint reports it.
// The editor reloads credit usage after every job, so the header follows each paid call.
export const spendStore = writable(null);

export function publishSpend(usage) {
	spendStore.set(usage && usage.provider_spend ? usage.provider_spend : null);
}

const cents = (n) => Math.round(Number(n) * 1e6) / 1e4;
// Spend rounds up and what is left rounds down, so the header never understates a cost.
const up = (n) => `$${(Math.ceil(cents(n) - 1e-9) / 100).toFixed(2)}`;
const down = (n) => `$${(Math.floor(cents(n) + 1e-9) / 100).toFixed(2)}`;

// What the header shows, or null when no cap is configured or the figures are unusable.
export function spendView(spend) {
	if (!spend || spend.enabled !== true) return null;
	const { cap_usd: cap, spent_usd: spent, remaining_usd: left } = spend;
	if (![cap, spent, left].every((n) => Number.isFinite(n) && n >= 0) || cap <= 0) return null;
	const level = left <= 0 ? 'out' : left < cap * 0.25 ? 'low' : 'ok';
	return {
		text: `${up(spent)} / ${down(cap)}`,
		short: up(spent),
		title: level === 'out'
			? `Spend cap reached: ${up(spent)} of ${down(cap)}. No further paid call is sent.`
			: `Spent ${up(spent)} of the ${down(cap)} cap. ${down(left)} left.`,
		level
	};
}
