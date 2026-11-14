// Reading a script from a TXT or Markdown file. The text is kept exactly as written (the project stores it
// as the original script); only a byte order mark, which is not text, is dropped.

// These mirror the server's limits (`DefaultRequestLimits` in backend/internal/api/request_limits.go:
// ScriptBytes and ScriptWords). The server enforces the real ones, which an operator can change, and its
// answer is shown if a file passes here but not there. tests/scriptImport.test.js checks these against the
// Go source so they cannot drift apart unnoticed.
export const MAX_SCRIPT_BYTES = 512 * 1024;
export const MAX_SCRIPT_WORDS = 100000;

export const SCRIPT_FILE_ACCEPT = '.txt,.md,.markdown,text/plain,text/markdown';
const TEXT_EXTENSIONS = ['.txt', '.md', '.markdown'];
const TEXT_TYPES = ['text/plain', 'text/markdown', 'text/x-markdown'];

export class ScriptImportError extends Error {}

// A file is rounded up, so one over the limit never reads as the limit.
const kb = (bytes, round = Math.round) => `${Math.max(1, round(bytes / 1024))} KB`;

export function countWords(text) {
	return text.trim() ? text.trim().split(/\s+/).length : 0;
}

function checkKind(file) {
	const name = (file.name || '').toLowerCase();
	if (name.endsWith('.pdf') || file.type === 'application/pdf') {
		throw new ScriptImportError('PDF files are not imported. Paste the text, or save the script as a TXT or Markdown file.');
	}
	const extension = TEXT_EXTENSIONS.find((suffix) => name.endsWith(suffix));
	if (!extension && !TEXT_TYPES.includes(file.type)) {
		throw new ScriptImportError('Choose a TXT or Markdown file (.txt, .md or .markdown).');
	}
}

// Resolves to { text, name, bytes, words }, or rejects with a ScriptImportError whose message says what to do.
export async function readScriptFile(file, limits = { bytes: MAX_SCRIPT_BYTES, words: MAX_SCRIPT_WORDS }) {
	checkKind(file);
	if (file.size > limits.bytes) {
		throw new ScriptImportError(`This file is ${kb(file.size, Math.ceil)}. A script can be at most ${kb(limits.bytes)}.`);
	}
	let text;
	try {
		text = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer());
	} catch {
		throw new ScriptImportError('This file is not UTF-8 text. Save it as plain text in UTF-8 and try again.');
	}
	if (text.charCodeAt(0) === 0xfeff) text = text.slice(1);
	if (text.includes('\0')) {
		throw new ScriptImportError('This file contains binary data, not text.');
	}
	if (!text.trim()) {
		throw new ScriptImportError('This file is empty.');
	}
	const words = countWords(text);
	if (words > limits.words) {
		throw new ScriptImportError(`This script has ${words.toLocaleString('en-US')} words. A script can have at most ${limits.words.toLocaleString('en-US')}.`);
	}
	return { text, name: file.name, bytes: file.size, words };
}
