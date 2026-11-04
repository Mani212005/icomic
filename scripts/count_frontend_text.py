from pathlib import Path
import re
import sys

root = Path(sys.argv[1])
words = re.compile(r"[\w]+(?:['’-][\w]+)*", re.UNICODE)
comment_re = re.compile(r'<!--.*?-->|/\*.*?\*/|^[ \t]*//[^\n]*$|[ \t]//[^\n]*$', re.M | re.S)
attr_re = re.compile(r'\b(?:title|aria-label|placeholder|alt)="([^"]*)"')
string_re = re.compile(r'''(?:"([^"\n]*)"|'([^'\n]*)'|`([^`\n]*)`)''')
ui = comments = readme = 0
for path in root.rglob('*'):
    if not path.is_file() or 'node_modules' in path.parts or '.svelte-kit' in path.parts or path.suffix not in {'.svelte', '.js', '.html', '.md'}:
        continue
    source = path.read_text(errors='ignore')
    if path.name == 'README.md':
        readme += len(words.findall(source))
        continue
    for match in comment_re.finditer(source):
        comments += len(words.findall(match.group()))
    source = comment_re.sub(' ', source)
    if path.suffix == '.svelte':
        script = re.search(r'<script\b[^>]*>(.*?)</script>', source, re.S)
        if script:
            ui += sum(len(words.findall(next((part for part in match.groups() if part is not None), ''))) for match in string_re.finditer(script.group(1)))
        source = re.sub(r'<script\b.*?</script>|<style\b.*?</style>', ' ', source, flags=re.S)
        ui += len(words.findall(re.sub(r'<[^>]+>|\{[^{}]*\}', ' ', source)))
        ui += sum(len(words.findall(value)) for value in attr_re.findall(source))
print(f'copy_words={ui}; readme_words={readme}; comment_words={comments}; total={ui + readme + comments}')
