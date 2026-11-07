import assert from 'node:assert/strict';
import { readdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';

const outputDir = 'build';
const forbidden = [
	/localhost:8080/i,
	/PUBLIC_API_URL/,
	/\b[A-Z][A-Z0-9_]*(?:API_KEY|AUTH_TOKEN|SECRET|ACCESS_TOKEN)\b/
];
const externalUrl = /https?:\/\/[^\s"'<>`)}\]]+/gi;
// The footer links to the public repository README and docs; nothing else outside the bundle is allowed.
const allowedUrl = /^(?:https?:\/\/www\.w3\.org\/(?:2000\/svg(?:%22)?|1999\/xhtml)$|https:\/\/svelte\.dev\/e\/|https:\/\/github\.com\/Mani212005\/icomic(?:[/#]|$))/i;

async function* filesUnder(directory) {
	for (const entry of await readdir(directory, { withFileTypes: true })) {
		const path = join(directory, entry.name);
		if (entry.isDirectory()) yield* filesUnder(path);
		else yield path;
	}
}

let scanned = 0;
for await (const file of filesUnder(outputDir)) {
	if (!/\.(?:js|html|json|css|mjs|cjs)$/.test(file)) continue;
	const content = await readFile(file);
	scanned += 1;
	const source = content.toString('utf8');
	for (const pattern of forbidden) {
		assert.equal(pattern.test(source), false, `Demo output ${file} contains forbidden API or secret reference: ${pattern}`);
	}
	for (const url of source.match(externalUrl) ?? []) {
		assert.match(url, allowedUrl, `Demo output ${file} contains a non-local URL: ${url}`);
	}
}

assert.ok(scanned > 0, `No web assets found in ${outputDir}`);
console.log(`Checked ${scanned} demo output files for live API and secret references.`);
