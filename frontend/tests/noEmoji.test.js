import test from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join, relative } from 'node:path';

// Include invisible presentation/joining characters and keycap sequences as well.
const emoji = /[\p{Extended_Pictographic}\p{Emoji_Presentation}\uFE0F\u200D\u20E3]/u;
const root = fileURLToPath(new URL('../', import.meta.url));
function scan(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return scan(path);
    return readFileSync(path, 'utf8').split('\n').flatMap((line, index) =>
      emoji.test(line) ? [`${relative(root, path)}:${index + 1}`] : []);
  });
}

test('frontend source and tests contain no emoji code points', () => {
  assert.deepEqual([...scan(join(root, 'src')), ...scan(join(root, 'tests'))], []);
  assert.equal(emoji.test(readFileSync(join(root, 'README.md'), 'utf8')), false);
});

test('emoji guard catches pictographs, flags, presentation selectors and keycaps', () => {
  for (const sample of ['\u{1F3A8}', '\u{1F1EE}\u{1F1F3}', '\u26A0\uFE0F', '1\uFE0F\u20E3']) {
    assert.equal(emoji.test(sample), true);
  }
  assert.equal(emoji.test('Script 1 - SVG icons'), false);
});
