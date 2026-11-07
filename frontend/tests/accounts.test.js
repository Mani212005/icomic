import test from 'node:test';
import assert from 'node:assert/strict';
import { ApiClient } from '../src/lib/api/client.js';
import { MockApiClient } from '../src/lib/api/mockClient.js';
import { loginErrorMessage, projectAccess } from '../src/lib/account/session.js';

function fakeServer(responses) {
	const requests = [];
	const original = globalThis.fetch;
	globalThis.fetch = async (url, options) => {
		requests.push({ url, options });
		const next = responses.shift();
		return {
			ok: next.status < 400,
			status: next.status,
			json: async () => next.body
		};
	};
	return { requests, restore: () => (globalThis.fetch = original) };
}

const guest = { kind: 'guest', email: null, project_count: 0, project_limit: 1, csrf_token: 'csrf-1' };

test('ApiClient sends the session cookie and echoes the CSRF token only on writes', async () => {
	const server = fakeServer([
		{ status: 200, body: { account: guest } },
		{ status: 200, body: { projects: [] } },
		{ status: 201, body: { project: { id: 'p1' } } }
	]);
	try {
		const client = new ApiClient({ baseUrl: 'http://api', devMode: false });
		assert.deepEqual(await client.getAccount(), guest);
		await client.listProjects();
		await client.createProject({ title: 'T', original_script: 'S' });
		const [me, list, create] = server.requests;
		assert.equal(me.options.credentials, 'include');
		assert.equal(list.options.headers['X-CSRF-Token'], undefined);
		assert.equal(create.options.headers['X-CSRF-Token'], 'csrf-1');
		assert.equal(create.options.credentials, 'include');
	} finally {
		server.restore();
	}
});

test('ApiClient starts a guest session lazily and only when there is none', async () => {
	const server = fakeServer([
		{ status: 200, body: { account: null } },
		{ status: 201, body: { account: guest } },
		{ status: 200, body: { account: guest } }
	]);
	try {
		const client = new ApiClient({ baseUrl: 'http://api', devMode: false });
		assert.deepEqual(await client.ensureSession(), guest);
		assert.equal(server.requests[1].url, 'http://api/api/v1/auth/guest');
		assert.equal(server.requests[1].options.method, 'POST');
		assert.deepEqual(await client.ensureSession(), guest);
		assert.equal(server.requests.length, 3, 'an existing session must not start another guest');
	} finally {
		server.restore();
	}
});

test('ApiClient sign-up, sign-in and sign-out keep the token in memory and never send the password elsewhere', async () => {
	const registered = { kind: 'registered', email: 'a@example.com', project_count: 1, project_limit: 100, csrf_token: 'csrf-2' };
	const server = fakeServer([
		{ status: 201, body: { account: registered } },
		{ status: 200, body: { account: registered, claimed_projects: 1 } },
		{ status: 204, body: null },
		{ status: 403, body: { code: 'csrf_invalid', message: 'Missing or invalid CSRF token.' } }
	]);
	try {
		const client = new ApiClient({ baseUrl: 'http://api', devMode: false });
		await client.signUp('a@example.com', 'a long enough password');
		assert.deepEqual(JSON.parse(server.requests[0].options.body), { email: 'a@example.com', password: 'a long enough password' });
		assert.equal(client.csrfToken, 'csrf-2');
		const result = await client.signIn('a@example.com', 'a long enough password');
		assert.equal(result.claimed_projects, 1);
		await client.signOut();
		assert.equal(server.requests[2].options.headers['X-CSRF-Token'], 'csrf-2');
		assert.equal(client.csrfToken, null);
		assert.equal(client.account, null);
		const storage = JSON.stringify(Object.keys(globalThis.localStorage ?? {}));
		assert.ok(!storage.includes('csrf'));
	} finally {
		server.restore();
	}
});

test('ApiClient exposes stable error codes and details for login and project limits', async () => {
	const server = fakeServer([
		{ status: 403, body: { code: 'project_limit_reached', message: 'Guests can keep one project.', details: { account_kind: 'guest', limit: 1 } } },
		{ status: 429, body: { code: 'login_rate_limited', message: 'Too many attempts.', retryable: true, retry_after_seconds: 42 } }
	]);
	try {
		const client = new ApiClient({ baseUrl: 'http://api', devMode: false });
		await assert.rejects(client.createProject({ title: 'T', original_script: 'S' }), (err) => {
			assert.equal(err.status, 403);
			assert.equal(err.code, 'project_limit_reached');
			assert.deepEqual(err.details, { account_kind: 'guest', limit: 1 });
			return true;
		});
		await assert.rejects(client.signIn('a@example.com', 'x'), (err) => {
			assert.equal(loginErrorMessage(err), 'Too many attempts. Retry in 42 seconds.');
			return true;
		});
	} finally {
		server.restore();
	}
});

test('loginErrorMessage maps stable codes and falls back to the server message', () => {
	assert.equal(loginErrorMessage({ code: 'invalid_credentials' }), 'Incorrect email or password.');
	assert.equal(loginErrorMessage({ code: 'email_taken' }), 'This email has an account. Sign in.');
	assert.equal(loginErrorMessage({ code: 'weak_password', message: 'use at least 10 characters' }), 'use at least 10 characters');
	assert.equal(loginErrorMessage(null), 'Something went wrong.');
});

test('projectAccess keeps the multi-project list and extra projects behind the login', () => {
	assert.deepEqual(projectAccess(null), { canSeeList: false, atLimit: false, needsLogin: false });
	assert.deepEqual(projectAccess({ kind: 'guest', project_count: 0, project_limit: 1 }), { canSeeList: false, atLimit: false, needsLogin: false });
	assert.deepEqual(projectAccess({ kind: 'guest', project_count: 1, project_limit: 1 }), { canSeeList: false, atLimit: true, needsLogin: true });
	assert.deepEqual(projectAccess({ kind: 'registered', project_count: 100, project_limit: 100 }), { canSeeList: true, atLimit: true, needsLogin: false });
	assert.deepEqual(projectAccess({ kind: 'registered', project_count: 500, project_limit: null }), { canSeeList: true, atLimit: false, needsLogin: false });
});

test('mock demo: a guest has one project, signing up lifts the limit and keeps the project', async () => {
	const client = new MockApiClient({ accounts: true });
	const before = await client.getAccount();
	assert.equal(before.kind, 'guest');
	assert.equal(before.project_count, 0, 'the shared sample is not the guest project');
	assert.ok((await client.listProjects()).some((p) => p.is_sample));

	const mine = await client.createProject({ title: 'Mine', original_script: 'A story.' });
	await assert.rejects(client.createProject({ title: 'Two', original_script: 'B' }), (err) => err.code === 'project_limit_reached' && err.status === 403);

	await client.deleteProject(mine.id);
	const again = await client.createProject({ title: 'Again', original_script: 'C' });

	await assert.rejects(client.signUp('nope', 'a long enough password'), { code: 'invalid_email' });
	await assert.rejects(client.signUp('a@example.com', 'short'), { code: 'weak_password' });
	const signedUp = await client.signUp('A@Example.com', 'a long enough password');
	assert.equal(signedUp.account.kind, 'registered');
	assert.equal(signedUp.account.email, 'a@example.com');
	assert.equal(signedUp.claimed_projects, 1);
	assert.ok((await client.listProjects()).some((p) => p.id === again.id));
	await client.createProject({ title: 'More', original_script: 'D' });
	await client.createProject({ title: 'Even more', original_script: 'E' });
	await assert.rejects(client.signUp('a@example.com', 'another long password'), { code: 'email_taken' });
});

test('mock demo: sign-out starts a fresh guest and sign-in restores the account projects', async () => {
	const client = new MockApiClient({ accounts: true });
	await client.signUp('a@example.com', 'a long enough password');
	const kept = await client.createProject({ title: 'Kept', original_script: 'A' });
	await client.signOut();
	assert.equal((await client.getAccount()).kind, 'guest');
	assert.ok(!(await client.listProjects()).some((p) => p.id === kept.id), 'account projects are not visible to the next guest');
	const guestProject = await client.createProject({ title: 'Guest', original_script: 'B' });

	await assert.rejects(client.signIn('a@example.com', 'wrong password!'), { code: 'invalid_credentials' });
	await assert.rejects(client.signIn('ghost@example.com', 'a long enough password'), { code: 'invalid_credentials' });
	const result = await client.signIn('a@example.com', 'a long enough password');
	assert.equal(result.claimed_projects, 1);
	const ids = (await client.listProjects()).map((p) => p.id);
	assert.ok(ids.includes(kept.id) && ids.includes(guestProject.id));
});

test('mock client without accounts keeps unlimited projects for existing tests', async () => {
	const client = new MockApiClient();
	for (let i = 0; i < 3; i++) await client.createProject({ title: `P${i}`, original_script: 'S' });
	assert.equal((await client.getAccount()).project_limit, null);
});
