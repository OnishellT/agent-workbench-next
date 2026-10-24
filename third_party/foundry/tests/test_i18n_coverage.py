"""Every message the backend can show the user has an English entry in ui/src/i18n/en.server.ts."""
import ast
import json
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parent.parent
DICTIONARY = ROOT / 'ui' / 'src' / 'i18n'
LINE = re.compile(r'^\s+("(?:[^"\\]|\\.)*"): ("(?:[^"\\]|\\.)*"),$')
SKIP_FILES = {'__main__.py'}  # command-line help and messages: not the UI


def norm(text):
    return ' '.join(text.split())


def keys():
    found = []
    for name in ('en.server.ts', 'en.ui.ts'):
        for line in (DICTIONARY / name).read_text().splitlines():
            match = LINE.match(line)
            if match:
                found.append(norm(json.loads(match.group(1))))
    return found


def literals(node):
    """String constants inside a message expression (a literal, a concatenation, an f-string)."""
    for child in ast.walk(node):
        if isinstance(child, ast.Constant) and isinstance(child.value, str):
            yield child.value


def messages():
    """(file, text) for what the UI shows: Invalid(...), readiness add(...) and job progress text."""
    for path in sorted((ROOT / 'foundry_loop').glob('*.py')):
        if path.name in SKIP_FILES:
            continue
        for node in ast.walk(ast.parse(path.read_text())):
            if not isinstance(node, ast.Call):
                continue
            name = node.func.id if isinstance(node.func, ast.Name) else getattr(node.func, 'attr', '')
            if name == 'Invalid' and node.args:
                target = node.args[0]
            elif name == 'add' and len(node.args) >= 3 and isinstance(node.args[1], ast.Constant):
                target = node.args[2]
            elif name == 'progress' and len(node.args) >= 2:
                target = node.args[1]
            else:
                continue
            for text in literals(target):
                text = norm(text)
                if len(text) >= 4 and (' ' in text or re.search('[áéíóúñ]', text)):
                    yield path.name, text


class I18nCoverage(unittest.TestCase):
    def test_every_backend_message_has_an_english_entry(self):
        known = keys()
        # A message built from pieces ("prefix: " + detail) is covered by one pattern that contains
        # the piece; a whole message is covered by its own entry.
        missing = sorted({f'{file}: {text}' for file, text in messages() if not any(text in key for key in known)})
        self.assertEqual(missing, [], 'add these to ui/src/i18n/en.server.ts')

    def test_patterns_use_distinct_placeholder_names(self):
        for key in keys():
            names = re.findall(r'\{(\w+)\}', key)
            self.assertEqual(len(names), len(set(names)), key)


if __name__ == '__main__':
    unittest.main()
