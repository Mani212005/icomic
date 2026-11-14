import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { MAX_SCRIPT_BYTES, MAX_SCRIPT_WORDS, ScriptImportError, countWords, readScriptFile } from '../src/lib/editor/scriptImport.js';

const file = (parts, name, type = '') => new File(parts, name, { type });
const refused = (promise, pattern) => assert.rejects(promise, (error) => error instanceof ScriptImportError && pattern.test(error.message));

test('the client limits are the backend defaults, so they cannot drift apart unnoticed', () => {
	const go = readFileSync(fileURLToPath(new URL('../../backend/internal/api/request_limits.go', import.meta.url)), 'utf8');
	const defaults = go.match(/func DefaultRequestLimits\(\) RequestLimits \{\s*return RequestLimits\{([^}]*)\}/)[1];
	assert.equal(MAX_SCRIPT_BYTES, 512 * 1024);
	assert.match(defaults, /ScriptBytes: 512 << 10/);
	assert.equal(MAX_SCRIPT_WORDS, Number(defaults.match(/ScriptWords: (\d+)/)[1]));
});

test('a TXT or Markdown file is read exactly as written', async () => {
	const markdown = '# Title\r\n\r\n  Nia finds a *bird*.\r\n\r\n- one\n- two\n\n';
	const read = await readScriptFile(file([markdown], 'story.md', 'text/markdown'));
	assert.equal(read.text, markdown, 'no trimming, line ending changes or Markdown stripping');
	assert.equal(read.name, 'story.md');
	assert.equal(read.bytes, markdown.length);
	assert.equal(read.words, countWords(markdown));
	assert.equal((await readScriptFile(file(['A hero.'], 'notes.TXT'))).text, 'A hero.');
	assert.equal((await readScriptFile(file(['A hero.'], 'notes.markdown'))).text, 'A hero.');
	assert.equal((await readScriptFile(file(['A hero.'], 'blob', 'text/plain'))).text, 'A hero.', 'a plain text type is enough without an extension');
});

test('non-ASCII text and a byte order mark', async () => {
	const text = 'Nía finds a 鳥 and a café.';
	assert.equal((await readScriptFile(file([text], 'a.txt'))).text, text);
	const bom = await readScriptFile(file([new Uint8Array([0xef, 0xbb, 0xbf]), 'A hero.'], 'a.txt'));
	assert.equal(bom.text, 'A hero.', 'the byte order mark is not part of the script');
	assert.equal(bom.bytes, 3 + 7);
});

test('files that are not scripts are refused with something to do about it', async () => {
	await refused(readScriptFile(file(['%PDF-1.4'], 'story.pdf', 'application/pdf')), /PDF files are not imported.*TXT or Markdown/);
	await refused(readScriptFile(file(['{}'], 'story.json', 'application/json')), /Choose a TXT or Markdown file/);
	await refused(readScriptFile(file(['   \n\t'], 'empty.txt')), /empty/);
	await refused(readScriptFile(file([new Uint8Array([0xff, 0xfe, 0x41, 0x00])], 'utf16.txt')), /not UTF-8/);
	await refused(readScriptFile(file(['A hero\0 finds a bird.'], 'binary.txt')), /binary data/);
});

test('a file over the byte or word limit is refused before it is used', async () => {
	const tooBig = file([new Uint8Array(MAX_SCRIPT_BYTES + 1).fill(97)], 'big.txt');
	await refused(readScriptFile(tooBig), /513 KB.*at most 512 KB/);
	const atLimit = file([new Uint8Array(MAX_SCRIPT_BYTES).fill(97)], 'limit.txt');
	assert.equal((await readScriptFile(atLimit)).bytes, MAX_SCRIPT_BYTES, 'the limit itself is allowed, as on the server');
	await refused(readScriptFile(file(['one two three four'], 'words.txt'), { bytes: 100, words: 3 }), /4 words.*at most 3/);
});
