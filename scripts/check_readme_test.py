"""Hermetic checks for the root README's image, links, and text rendering."""

from __future__ import annotations

import re
import unittest
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parent.parent
README = ROOT / "README.md"
MARKDOWN_LINK = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
EMOJI_RANGES = (
    (0x1F000, 0x1FAFF),
    (0x1FC00, 0x1FFFD),
    (0x2600, 0x27BF),
    (0x2300, 0x23FF),
    (0xFE00, 0xFE0F),
    (0x1F1E6, 0x1F1FF),
)


def is_emoji_character(char: str) -> bool:
    codepoint = ord(char)
    return char == "\u200d" or any(start <= codepoint <= end for start, end in EMOJI_RANGES)


def local_markdown_targets(markdown: str) -> list[str]:
    targets = []
    for match in MARKDOWN_LINK.finditer(markdown):
        destination = match.group(1).strip()
        if destination.startswith("<"):
            destination = destination[1 : destination.find(">")]
        else:
            destination = destination.split(maxsplit=1)[0]
        parsed = urlsplit(destination)
        if parsed.scheme or parsed.netloc or not parsed.path:
            continue
        targets.append(unquote(parsed.path))
    return targets


class ReadmeChecks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.content = README.read_text(encoding="utf-8")

    def test_readme_contains_no_emoji(self):
        found = sorted({char for char in self.content if is_emoji_character(char)})
        self.assertEqual(found, [], f"README contains emoji characters: {found}")

    def test_every_relative_link_and_image_path_exists(self):
        missing = [
            target
            for target in local_markdown_targets(self.content)
            if not (ROOT / target).exists()
        ]
        self.assertEqual(missing, [], f"README has missing relative paths: {missing}")


if __name__ == "__main__":
    unittest.main()
