"""Conservative, deterministic secret redaction before persistence or transport.

No detector can prove arbitrary prose contains no secret. Known credential forms,
secret assignments and suspicious opaque tokens are masked; unsupported values,
limits and residual matches fail closed. Offsets are Python Unicode character
positions in the *input* text. Metadata masks include a digest to retain identity.
"""
from __future__ import annotations

from bisect import bisect_right
import hashlib
import json
import math
import re

REDACTION_VERSION = "foundry-redact-v1"
_MAX_CHARS = 1_048_576
_MAX_NODES = 20_000
_SECRET_NAME = re.compile(
    r"(?:[A-Za-z][A-Za-z0-9_-]*[_-])?(?:private[_ -]?key|secret[_ -]?access[_ -]?key|connection[_ -]?string|api[_ -]?key|access[_ -]?token|refresh[_ -]?token|auth[_ -]?token|"
    r"client[_ -]?secret|password|passwd|pwd|secret|token|credentials?|authorization)",
    re.I,
)
_PLACEHOLDER = re.compile(r"\[REDACTED:[a-z_]+(?::[0-9a-f]{64})?\]")
_PATTERNS = (
    ("pem", re.compile(r"-----BEGIN [A-Z0-9 ]+-----[\s\S]*?(?:-----END [A-Z0-9 ]+-----|\Z)"), 0),
    ("credential", re.compile(r"(?<![\w-])(?:sk-(?:proj-|live-|test-)?[A-Za-z0-9_-]{8,}|(?:gh[pousr]_|github_pat_)[A-Za-z0-9_]{8,}|xox[baprs]-[A-Za-z0-9-]{8,}|(?:AKIA|ASIA)[A-Z0-9]{16}|AIza[A-Za-z0-9_-]{30,})(?![\w-])"), 0),
    ("jwt", re.compile(r"(?<![\w.-])eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+(?![\w.-])"), 0),
    ("authorization", re.compile(r"\b(?:Bearer|Basic)[ \t]+([A-Za-z0-9+/_.~=-]+)", re.I), 1),
    ("url_credentials", re.compile(r"\b[a-z][a-z0-9+.-]*://([^\s/@]+(?::[^\s/@]*)?@)", re.I), 1),
    ("assignment", re.compile(r'''(?ix)(?<![\w-])(?:[A-Za-z][A-Za-z0-9_-]*[_-])?(?:private[_\ -]?key|secret[_\ -]?access[_\ -]?key|connection[_\ -]?string|api[_\ -]?key|access[_\ -]?token|refresh[_\ -]?token|auth[_\ -]?token|client[_\ -]?secret|password|passwd|pwd|secret|token|credentials?|authorization)["']?[ \t]*(?:=|:|\bis\b)[ \t]*(?P<value>"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\n\r,;\}\]]+)'''), "value"),
    ("opaque_token", re.compile(r"(?<![\w+/=-])[A-Za-z0-9+/_=-]{40,}(?![\w+/=-])"), 0),
)


# Block/triple/unclosed assignments cannot be made safe by masking only their
# first line. A missing terminator conservatively consumes the remainder.
_ASSIGN_PREFIX = r"""(?<![\w-])""" + _SECRET_NAME.pattern + r"""["']?[ \t]*(?:=|:|\bis\b)[ \t]*"""
_PATTERNS = (
    ("assignment_block", re.compile(_ASSIGN_PREFIX + r"(?:(?:[|>][-+]?[ \t]*)?\r?\n)(?P<value>(?:[ \t]+[^\r\n]*(?:\r?\n|\Z))+|[^\r\n]+)", re.I), "value"),
    ("assignment_triple", re.compile(_ASSIGN_PREFIX + r"(?P<value>\x22{3}[\s\S]*?(?:\x22{3}|\Z)|\x27{3}[\s\S]*?(?:\x27{3}|\Z))", re.I), "value"),
    ("assignment_unclosed", re.compile(_ASSIGN_PREFIX + r"(?P<value>\x22(?:\\.|[^\x22\\])*\Z|\x27(?:\\.|[^\x27\\])*\Z)", re.I), "value"),
) + _PATTERNS


def _spans(text: str) -> list[tuple[int, int, str]]:
    placeholders = [(m.start(), m.end()) for m in _PLACEHOLDER.finditer(text)]
    placeholder_starts = [left for left, _ in placeholders]
    spans = []
    for kind, pattern, group in _PATTERNS:
        for match in pattern.finditer(text):
            start, end = match.span(group)
            token = text[start:end]
            if kind == "assignment_block":
                # Keep the following nonsecret line structurally separate.
                while end > start and text[end - 1] in "\r\n":
                    end -= 1
                token = text[start:end]
            if kind == "assignment":
                if len(token) >= 2 and token[0] in "\"'" and token[-1] == token[0]:
                    start += 1
                    end -= 1
                    token = text[start:end]
                if not token:
                    continue
            index = bisect_right(placeholder_starts, start) - 1
            if index >= 0 and end <= placeholders[index][1]:
                continue
            if kind == "opaque_token":
                # Hashes are provenance, not opaque bearer material. Secret-key
                # assignments of hex values are still caught by the other rule.
                if re.fullmatch(r"[0-9a-fA-F]{40,}", token):
                    continue
                if not (re.search(r"[a-z]", token) and re.search(r"[A-Z]", token) and re.search(r"[0-9]", token)):
                    continue
            spans.append((start, end, kind))
    merged = []
    for start, end, kind in sorted(spans, key=lambda s: (s[0], -s[1], s[2])):
        if merged and start < merged[-1][1]:
            old_start, old_end, old_kind = merged[-1]
            merged[-1] = (old_start, max(end, old_end), old_kind)
        else:
            merged.append((start, end, kind))
    return merged


def _valid_text(text: str) -> bool:
    if len(text) > _MAX_CHARS or any(ord(c) < 32 and c not in "\n\r\t" for c in text):
        return False
    try:
        text.encode("utf-8", errors="strict")
    except UnicodeError:
        return False
    return True


def _mask(text: str, *, identity: bool = False) -> tuple[str, list[dict]]:
    if not _valid_text(text):
        raise ValueError("unsupported redaction input")
    spans = _spans(text)
    chunks = []
    end_previous = 0
    for start, end, kind in spans:
        chunks.append(text[end_previous:start])
        suffix = ":" + hashlib.sha256(text[start:end].encode("utf-8")).hexdigest() if identity else ""
        chunks.append(f"[REDACTED:{kind}{suffix}]")
        end_previous = end
    chunks.append(text[end_previous:])
    return "".join(chunks), [{"kind": kind, "start": start, "end": end} for start, end, kind in spans]


def _secret_mask(value) -> str:
    raw = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode("utf-8")
    return "[REDACTED:metadata:" + hashlib.sha256(raw).hexdigest() + "]"


def _walk(value, *, sanitize: bool, depth: int = 0, budget: list[int]):
    budget[0] -= 1
    if depth > 20 or budget[0] < 0:
        raise ValueError("redaction limits exceeded")
    if isinstance(value, str):
        budget[1] -= len(value)
        if budget[1] < 0 or not _valid_text(value):
            raise ValueError("unsupported redaction input")
        if sanitize:
            return _mask(value, identity=True)[0]
        if _spans(value):
            raise ValueError("outbound content failed secret screening")
        return value
    if value is None or type(value) in (bool, int):
        return value
    if type(value) is float and math.isfinite(value):
        return value
    if isinstance(value, list):
        return [_walk(v, sanitize=sanitize, depth=depth + 1, budget=budget) for v in value]
    if isinstance(value, dict):
        result = {}
        for key, item in value.items():
            if not isinstance(key, str):
                raise ValueError("unsupported redaction input")
            clean_key = _walk(key, sanitize=sanitize, depth=depth + 1, budget=budget)
            if _SECRET_NAME.fullmatch(key) and item is not None and item is not False and not (isinstance(item, str) and not item):
                if sanitize and isinstance(item, str) and _PLACEHOLDER.fullmatch(item):
                    clean_item = item
                elif sanitize:
                    # Validate limits/types before hashing a sensitive subtree.
                    clean_item = _walk(item, sanitize=True, depth=depth + 1, budget=budget)
                    # Hash the original only after its shape has passed bounds.
                    clean_item = _secret_mask(item)
                elif isinstance(item, str) and _PLACEHOLDER.fullmatch(item):
                    clean_item = item
                else:
                    raise ValueError("outbound content failed secret screening")
            else:
                clean_item = _walk(item, sanitize=sanitize, depth=depth + 1, budget=budget)
            if clean_key in result:
                raise ValueError("redacted metadata key collision")
            result[clean_key] = clean_item
        return result
    raise ValueError("unsupported redaction input")


def assert_safe(value) -> None:
    """Screen a JSON-compatible outbound value; exceptions never echo its data."""
    _walk(value, sanitize=False, budget=[_MAX_NODES, 2 * _MAX_CHARS])


def sanitize(record: dict) -> dict:
    """Return only a screened copy; unsafe failures contain no original data."""
    try:
        if not isinstance(record, dict) or not isinstance(record.get("text"), str):
            raise ValueError("invalid record")
        text, redactions = _mask(record["text"])
        clean = _walk({key: value for key, value in record.items() if key != "text"}, sanitize=True, budget=[_MAX_NODES, 2 * _MAX_CHARS])
        clean.update(text=text, redactions=redactions, redaction_version=REDACTION_VERSION, safe=True)
        assert_safe(clean)
        return clean
    except (ValueError, TypeError, UnicodeError, RecursionError, OverflowError):
        return {"text": "[REDACTED:invalid]", "redactions": [], "redaction_version": REDACTION_VERSION, "safe": False}
