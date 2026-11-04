"""Script pacing shared by analysis and the fake runner.

Five panels form a normal page. Short scenes keep four panels. Stories of
3,000-6,000 words expand to 50-60 panels; longer stories continue growing.
There is no upper panel or chapter cap.
"""

import math

PANELS_PER_PAGE = 5
SOURCE_WORDS_PER_CALL = 500


def panel_count(word_count: int) -> int:
    if word_count < 3000:
        return max(4, math.ceil(word_count / 60))
    if word_count <= 6000:
        return 50 + math.ceil((word_count - 3000) / 300)
    return math.ceil(word_count / 100)


def chapter_count(word_count: int) -> int:
    return max(1, math.ceil(panel_count(word_count) / 15))


def split_text(text: str, count: int) -> list[str]:
    """Partition all words in order, including the remainder, without truncation."""
    words = text.split()
    return [
        " ".join(words[i * len(words) // count : (i + 1) * len(words) // count])
        for i in range(count)
    ]
