import { afterEach, expect, it, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import LoginForm from '../../src/lib/account/LoginForm.svelte';

afterEach(cleanup);

function setup(overrides = {}) {
	const client = {
		signIn: vi.fn().mockResolvedValue({ account: { kind: 'registered' } }),
		signUp: vi.fn().mockResolvedValue({ account: { kind: 'registered' } }),
		...overrides
	};
	const done = vi.fn();
	const view = render(LoginForm, { client, done, note: '' });
	return { client, done, view };
}

async function fill(view, email, password) {
	await fireEvent.input(view.getByLabelText('Email'), { target: { value: email } });
	await fireEvent.input(view.getByLabelText('Password'), { target: { value: password } });
}

it('signs in with the typed credentials and clears the password afterwards', async () => {
	const { client, done, view } = setup();
	await fill(view, 'a@example.com', 'a long enough password');
	await fireEvent.click(view.getByRole('button', { name: 'Continue' }));
	await vi.waitFor(() => expect(done).toHaveBeenCalled());
	expect(client.signIn).toHaveBeenCalledWith('a@example.com', 'a long enough password');
	expect(client.signUp).not.toHaveBeenCalled();
	expect(view.getByLabelText('Password').value).toBe('');
});

it('switches to sign up with the right browser hints and sends sign-up', async () => {
	const { client, done, view } = setup();
	expect(view.getByLabelText('Password').getAttribute('autocomplete')).toBe('current-password');
	await fireEvent.click(view.getByRole('button', { name: 'Sign up' }));
	const password = view.getByLabelText('Password');
	expect(password.getAttribute('autocomplete')).toBe('new-password');
	expect(password.getAttribute('minlength')).toBe('10');
	expect(view.getByText('At least 10 characters.')).toBeTruthy();
	expect(view.getByRole('button', { name: 'Sign up' }).getAttribute('aria-pressed')).toBe('true');
	await fill(view, 'new@example.com', 'a long enough password');
	await fireEvent.click(view.getByRole('button', { name: 'Continue' }));
	await vi.waitFor(() => expect(done).toHaveBeenCalled());
	expect(client.signUp).toHaveBeenCalledWith('new@example.com', 'a long enough password');
});

it('shows a stable-coded failure in an alert without revealing which part was wrong', async () => {
	const { done, view } = setup({
		signIn: vi.fn().mockRejectedValue(Object.assign(new Error('x'), { code: 'invalid_credentials' }))
	});
	await fill(view, 'a@example.com', 'whatever password');
	await fireEvent.click(view.getByRole('button', { name: 'Continue' }));
	const alert = await view.findByRole('alert');
	expect(alert.textContent).toContain('Incorrect email or password.');
	expect(done).not.toHaveBeenCalled();
	expect(view.getByRole('button', { name: 'Continue' }).disabled).toBe(false);
});

it('shows the retry delay when rate limited', async () => {
	const { view } = setup({
		signIn: vi.fn().mockRejectedValue(Object.assign(new Error('x'), { code: 'login_rate_limited', retry_after_seconds: 30 }))
	});
	await fill(view, 'a@example.com', 'whatever password');
	await fireEvent.click(view.getByRole('button', { name: 'Continue' }));
	expect((await view.findByRole('alert')).textContent).toContain('Retry in 30 seconds.');
});

it('disables submit while a request is in flight and shows the note', async () => {
	let finish;
	const pending = new Promise((resolve) => (finish = resolve));
	const client = { signIn: vi.fn().mockReturnValue(pending), signUp: vi.fn() };
	const view = render(LoginForm, { client, note: 'Your project stays with you.' });
	expect(view.getByText('Your project stays with you.')).toBeTruthy();
	await fill(view, 'a@example.com', 'a long enough password');
	await fireEvent.click(view.getByRole('button', { name: 'Continue' }));
	expect(view.getByRole('button', { name: 'Continue' }).disabled).toBe(true);
	finish({ account: {} });
	await vi.waitFor(() => expect(view.getByRole('button', { name: 'Continue' }).disabled).toBe(false));
});

it('uses a password input and labelled fields', () => {
	const { view } = setup();
	expect(view.getByLabelText('Password').getAttribute('type')).toBe('password');
	expect(view.getByLabelText('Email').getAttribute('type')).toBe('email');
	expect(view.getByRole('heading', { name: 'Account' })).toBeTruthy();
});
