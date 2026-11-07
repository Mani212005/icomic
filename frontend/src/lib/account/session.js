import { createDefaultClient } from '#lib/api/index.js';

export const LOGIN_LIMIT_URL = '/login?limit';

let client;

// One client for every route, so the session and mock account survive navigation.
export function sharedClient() {
	client ??= createDefaultClient();
	return client;
}

export function resetSharedClient(next) {
	client = next;
}

// What the visitor may do next, from the account the server reports.
export function projectAccess(account) {
	if (!account) return { canSeeList: false, atLimit: false, needsLogin: false };
	const atLimit = account.project_limit !== null && account.project_count >= account.project_limit;
	return {
		canSeeList: account.kind === 'registered',
		atLimit,
		needsLogin: atLimit && account.kind !== 'registered'
	};
}

const LOGIN_MESSAGES = {
	invalid_credentials: 'Incorrect email or password.',
	email_taken: 'This email has an account. Sign in.',
	invalid_email: 'Enter a valid email address.',
	login_rate_limited: 'Too many attempts.'
};

export function loginErrorMessage(err) {
	const base = LOGIN_MESSAGES[err?.code] || err?.message || 'Something went wrong.';
	const wait = Math.ceil(Number(err?.retry_after_seconds));
	return err?.code === 'login_rate_limited' && wait > 0 ? `${base} Retry in ${wait} seconds.` : base;
}
