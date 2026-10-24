"""Bounded capture of explicitly registered conversation exports.

Transcripts and attachments are inert evidence. This module never loads credentials,
executes transcript instructions, labels outcomes, or invokes inference. Event indices
and episode endpoints are zero-based; last_event is inclusive.
"""
from __future__ import annotations

import hashlib
import heapq
import json
import math
import os
import re
import stat
import time
from datetime import datetime, timezone
from pathlib import Path

from .common import Invalid, clean, digest, js, now, uid
from .vendor.foundry.redact import sanitize

ADAPTERS = {'codex', 'claude', 'pi', 'omp', 'canonical'}
ALIASES = {'codex_jsonl': 'codex', 'codex-jsonl': 'codex', 'claude-code': 'claude', 'jsonl': 'canonical'}
EXTENSIONS = {'.json', '.jsonl', '.ndjson'}
NORMALIZER_VERSION = 'foundry-capture-v4-live-prefix'
DISPLAY_METADATA_VERSION = 1
# Explicit directory registration is not permission to ingest credential files.
EXCLUDED = {'auth.json', 'credentials.json', 'settings.json', 'config.json', 'package.json', 'package-lock.json'}


def _json(raw):
    """Parse one record strictly: UTF-8, finite numbers, unique keys, bounded depth/size, object root.

    Raises ValueError (Invalid) with a short reason so callers can record an issue instead of trusting the record.
    """
    def pairs(items):
        value = {}
        for key, item in items:
            if key in value:
                raise Invalid('duplicate_json_key')
            value[key] = item
        return value
    def nonfinite(_):
        raise Invalid('nonfinite_json')
    try:
        value = json.loads(raw.decode('utf-8', errors='strict'), object_pairs_hook=pairs, parse_constant=nonfinite)
    except Invalid:
        raise
    except (UnicodeError, ValueError, RecursionError):
        raise Invalid('malformed_json') from None
    nodes = 0
    def bounded(item, depth=0):
        nonlocal nodes
        nodes += 1
        if depth > 20 or nodes > 20_000:
            raise Invalid('structure_limit')
        if isinstance(item, str):
            try:
                item.encode('utf-8', errors='strict')
            except UnicodeError:
                raise Invalid('invalid_unicode') from None
        elif isinstance(item, dict):
            for key, child in item.items():
                bounded(key, depth + 1)
                bounded(child, depth + 1)
        elif isinstance(item, list):
            for child in item:
                bounded(child, depth + 1)
    bounded(value)
    if not isinstance(value, dict):
        raise Invalid('record_not_object')
    return value


def register_source(store, path, adapter='canonical'):
    adapter = ALIASES.get(adapter, adapter)
    if adapter not in ADAPTERS:
        raise Invalid('Adaptador no soportado: codex, claude, pi, omp o canonical.')
    p = Path(path).expanduser().absolute()
    if p.is_symlink() or not (p.is_file() or p.is_dir()):
        raise Invalid('La fuente debe ser un archivo o directorio existente, sin enlaces simbólicos.')
    p = p.resolve()
    if p.name.lower() in EXCLUDED or (p.is_file() and p.suffix.lower() not in EXTENSIONS):
        raise Invalid('Selecciona un archivo de conversación JSON/JSONL, no credenciales o configuración.')
    old = store.one('SELECT * FROM sources WHERE path=?', (str(p),))
    if old:
        if old['adapter'] != adapter:
            raise Invalid('La ruta ya está registrada con otro adaptador.')
        store.execute('UPDATE sources SET enabled=1 WHERE id=?', (old['id'],))
        return store.one('SELECT * FROM sources WHERE id=?', (old['id'],))
    id = uid('source')
    store.execute('INSERT INTO sources VALUES (?,?,?,?,?,?)', (id, str(p), adapter, 1, now(), None))
    store.audit('source_registered', {'source_id': id, 'adapter': adapter})
    return store.one('SELECT * FROM sources WHERE id=?', (id,))


def discover_sources(store):
    """Connect standard session folders from this machine, never credentials."""
    candidates = [
        ('codex', Path(os.environ.get('CODEX_HOME', Path.home()/'.codex'))/'sessions'),
        ('claude', Path(os.environ.get('CLAUDE_CONFIG_DIR', Path.home()/'.claude'))/'projects'),
        ('pi', Path(os.environ.get('PI_CODING_AGENT_DIR', Path.home()/'.pi/agent'))/'sessions'),
        ('omp', Path(os.environ.get('OMP_AGENT_DIR', Path.home()/'.omp/agent'))/'sessions'),
    ]
    rows, errors = [], []
    for adapter, path in candidates:
        if path.is_dir():
            try:
                rows.append(register_source(store, str(path), adapter))
            except (Invalid, OSError) as exc:
                errors.append({'adapter': adapter, 'error': str(exc)})
    return {'sources': rows, 'errors': errors}


def relink_source(store, id, path):
    """Move a source while retaining its lineage, immutable evidence and labels."""
    source = store.one('SELECT * FROM sources WHERE id=?', (id,))
    if not source:
        raise Invalid('Fuente desconocida.')
    target = Path(path).expanduser().absolute()
    if target.is_symlink() or not (target.is_file() or target.is_dir()):
        raise Invalid('Selecciona una carpeta o archivo existente, sin enlaces simbólicos.')
    target = target.resolve()
    if target.name.lower() in EXCLUDED or (target.is_file() and target.suffix.lower() not in EXTENSIONS):
        raise Invalid('Selecciona conversaciones JSON/JSONL, no configuración ni credenciales.')
    duplicate = store.one('SELECT id FROM sources WHERE path=? AND id<>?', (str(target), id))
    if duplicate:
        raise Invalid('La ruta ya pertenece a otra fuente.')
    previous = Path(source['path'])
    sessions = store.rows('SELECT id,path FROM sessions WHERE source_id=?', (id,))
    # A file source has one identical path; a directory source has child paths.
    was_file = bool(sessions) and all(Path(row['path']) == previous for row in sessions)
    if sessions and was_file != target.is_file():
        raise Invalid('Conserva el tipo de fuente: archivo o carpeta.')
    with store.transaction():
        for row in sessions:
            old_path = Path(row['path'])
            try:
                new_path = target if was_file else target / old_path.relative_to(previous)
            except ValueError:
                raise Invalid('Una conversación tiene una ruta fuera de esta fuente.') from None
            store.execute('UPDATE sessions SET path=? WHERE id=?', (str(new_path), row['id']))
        store.execute('UPDATE sources SET path=?,error=NULL WHERE id=?', (str(target), id))
        store.execute('DELETE FROM file_marks WHERE path LIKE ?', (id + ':%',))
        for prefix in ('capture_cursor:', 'capture_recent_attempts:', 'capture_attempt:', 'capture_priority_cycle:'):
            store.execute('DELETE FROM kv WHERE key=?', (prefix + id,))
        store.audit('source_relinked', {'source_id': id, 'sessions_preserved': len(sessions)})
    return store.one('SELECT * FROM sources WHERE id=?', (id,))


def _descriptor(value):
    """Retain an attachment descriptor without copying binary payloads."""
    if not isinstance(value, dict):
        return {'kind': 'attachment', 'description': str(value)[:1000]}
    keep = {k: value[k] for k in ('type', 'mime_type', 'media_type', 'name', 'filename', 'path', 'url', 'image_url') if k in value}
    for k in ('url', 'image_url'):
        if isinstance(keep.get(k), dict):
            keep[k] = keep[k].get('url', '')
        if isinstance(keep.get(k), str) and keep[k].startswith('data:'):
            keep[k] = '[embedded media omitted]'
    source = value.get('source')
    if isinstance(source, dict):
        keep.update({k: source[k] for k in ('type', 'media_type', 'url') if k in source})
    if isinstance(keep.get('url'), str) and keep['url'].startswith('data:'):
        keep['url'] = '[embedded media omitted]'
    keep['kind'] = 'attachment'
    keep['inert'] = True
    return keep


def _content(value):
    text, attachments, tools = [], [], []
    if isinstance(value, str):
        return value, attachments, tools
    if isinstance(value, dict):
        value = [value]
    if not isinstance(value, list):
        return '' if value is None else js(value), attachments, tools
    for part in value:
        if isinstance(part, str):
            text.append(part)
        elif isinstance(part, dict):
            typ = part.get('type', '')
            if typ in ('text', 'input_text', 'output_text', 'thinking'):
                text.append(str(part.get('text', part.get('thinking', ''))))
            elif typ in ('tool_use', 'tool_result', 'toolCall', 'tool_call'):
                tools.append(part)
            elif typ in ('image', 'image_url', 'input_image', 'file', 'input_file', 'audio', 'video'):
                attachments.append(_descriptor(part))
            else:
                attachments.append({'kind': 'unsupported_content', 'type': str(typ), 'inert': True,
                                    'raw_content': part, 'raw_preserved': True})
        else:
            attachments.append({'kind': 'unsupported_content', 'type': type(part).__name__,
                                'inert': True, 'raw_content': part, 'raw_preserved': True})
    return '\n'.join(text), attachments, tools


def _normalize(records, adapter, fallback):
    events, issues, context = [], [], {}
    source_record, preserved_lines = {}, set()
    def evidence(value):
        # Keep JSON evidence, except intentionally excluded embedded media. Never
        # reinterpret historical instructions as executable/current instructions.
        if isinstance(value, list):
            return [evidence(item) for item in value]
        if not isinstance(value, dict):
            if isinstance(value, str) and value.startswith('data:'):
                return {'inert': True, 'kind': 'embedded_media_descriptor',
                        'sha256': hashlib.sha256(value.encode()).hexdigest(), 'characters': len(value)}
            return value
        if value.get('type') == 'credential_pin':
            public = {key: value[key] for key in ('type', 'id', 'parentId', 'timestamp', 'provider') if key in value}
            # Credential-pinning formats can add arbitrary key fields; wrapping
            # the remainder uses sanitize's sensitive-subtree masking.
            public['credentials'] = {key: item for key, item in value.items() if key not in public}
            return public
        if value.get('type') in ('image', 'image_url', 'input_image', 'file', 'input_file', 'audio', 'video'):
            result = _descriptor(value)
            result['descriptor_only'] = True
            result['source_sha256'] = digest(value)
            return result
        return {key: evidence(item) for key, item in value.items()}

    def emit(record, line, role='service', kind='message', text='', **extra):
        native_id = record.get('id') or record.get('message_id') or record.get('uuid')
        sid = record.get('session_id') or record.get('sessionId') or context.get('session_id') or fallback
        event = {'id': str(native_id) if native_id else 'generated_' + digest([fallback, line, len(events), kind])[:24],
                 'id_origin': 'native' if native_id else 'generated', 'session_id': str(sid),
                 'session_id_origin': 'native' if sid != fallback else 'generated',
                 'sequence': len(events), 'native_sequence': record.get('sequence', source_record.get('ordinal')), 'line': line, 'role': str(role), 'kind': kind,
                 'text': str(text), 'inert': True, 'timestamp': record.get('timestamp', source_record.get('timestamp')),
                 'parent_session_id': record.get('parent_session_id', context.get('parent_session_id')),
                 'parent_event_id': record.get('parentId', record.get('parentUuid')),
                 'branch_id': record.get('branch_id'), 'semantic_support': 'specialized', **extra}
        if line not in preserved_lines:
            event['raw_record'] = evidence(source_record)
            event['raw_preserved'] = True
            event['preservation_policy'] = 'sanitized_json_with_embedded_media_descriptors'
        else:
            event['raw_record_ref_line'] = line
        screened = sanitize(event)
        if not screened['safe']:
            issues.append({'line': line, 'reason': 'redaction_limit'})
            return
        events.append(screened)
        preserved_lines.add(line)

    def preserve(record, line, reason, native_type):
        before = len(events)
        emit(record, line, kind='native_event', text='', native_type=str(native_type),
             semantic_support='raw_only', completion_suggested=False)
        if len(events) > before:
            issues.append({'line': line, 'reason': reason, 'type': clean(str(native_type))[:100],
                           'retained': True, 'severity': 'semantic'})
    def message(record, line, msg):
        role = msg.get('role', record.get('role', record.get('type', 'service')))
        if role not in ('user', 'assistant', 'system', 'developer', 'tool', 'service', 'toolResult'):
            role = 'service'
        text, attachments, tools = _content(msg.get('content', msg.get('text', record.get('text', ''))))
        attachments += [_descriptor(x) for x in record.get('attachments', [])] if isinstance(record.get('attachments'), list) else []
        for attachment in attachments:
            if attachment.get('kind') == 'unsupported_content':
                issues.append({'line': line, 'reason': 'unsupported_content', 'type': clean(attachment['type'])[:100],
                               'retained': True, 'severity': 'semantic'})
        channel = msg.get('channel', msg.get('phase', record.get('channel')))
        if channel == 'final_answer':
            channel = 'final'
        if text or attachments or not tools:
            emit(record, line, 'tool' if role == 'toolResult' else role,
                 'tool_result' if role in ('toolResult', 'tool') else 'message', text,
                 attachments=attachments, channel=channel, completion_suggested=channel == 'final',
                 call_id=msg.get('toolCallId', msg.get('call_id')), tool_name=msg.get('toolName'))
        for i, tool in enumerate(tools):
            is_result = tool.get('type') == 'tool_result'
            body = tool.get('content', '') if is_result else tool.get('input', tool.get('arguments', ''))
            emit(dict(record, id=tool.get('id')), line, 'tool', 'tool_result' if is_result else 'tool_call',
                 body if isinstance(body, str) else js(body), tool_name=tool.get('name'),
                 call_id=tool.get('tool_use_id', tool.get('id')), content_part=i)
    for line, r in records:
        source_record = r
        native_session = r.get('session_id') or r.get('sessionId')
        if native_session:
            context['session_id'] = native_session
        typ = r.get('type', r.get('kind', ''))
        if typ in ('session_meta', 'session'):
            meta = r.get('payload', r)
            if isinstance(meta, dict):
                context['session_id'] = meta.get('id', meta.get('session_id', fallback))
                context['parent_session_id'] = meta.get('parent_session_id', meta.get('parentSessionId'))
                emit(meta, line, kind='session_metadata', text='', metadata={k: meta[k] for k in ('cwd', 'version', 'source', 'parent_session_id') if k in meta})
            else:
                preserve(r, line, 'invalid_metadata', typ)
            continue
        if adapter == 'codex':
            payload = r.get('payload')
            if not isinstance(payload, dict):
                preserve(r, line, 'unsupported_codex_event', typ); continue
            item = payload.get('type', '')
            completed = payload.get('item')
            if (typ == 'item_completed' or item == 'item_completed') and isinstance(completed, dict) and completed.get('type') in ('AgentMessage', 'UserMessage'):
                role = 'assistant' if completed['type'] == 'AgentMessage' else 'user'
                message(dict(completed, timestamp=r.get('timestamp')), line, dict(completed, role=role))
            elif item in ('function_call', 'custom_tool_call', 'tool_call', 'function_call_output', 'custom_tool_call_output', 'tool_result'):
                result = item.endswith('_output') or item == 'tool_result'
                body = payload.get('output', payload.get('text', '')) if result else payload.get('arguments', payload.get('input', ''))
                emit(payload, line, 'tool', 'tool_result' if result else 'tool_call', body if isinstance(body, str) else js(body), tool_name=payload.get('name'), call_id=payload.get('call_id'))
            elif typ == 'response_item' and item == 'message':
                message(dict(payload, timestamp=r.get('timestamp')), line, payload)
            elif typ == 'response_item' and item == 'agent_message':
                # Current Codex stores the body in content, not message. Its
                # explicit phase/channel can suggest completion; an unknown
                # author remains metadata rather than an invented identity.
                author = payload.get('role', payload.get('author'))
                role = author if author in ('user', 'assistant', 'system', 'developer', 'tool') else 'assistant'
                message(dict(payload, timestamp=r.get('timestamp')), line, dict(payload, role=role))
            elif item in ('task_complete', 'turn_end'):
                emit(payload, line, kind='completion', text=payload.get('last_agent_message', ''), completion_suggested=True, boundary=item)
            elif typ == 'event_msg' and item in ('user_message', 'agent_message'):
                body, attachments, tools = _content(payload.get('message', payload.get('content', payload.get('text', ''))))
                emit(payload, line, 'user' if item == 'user_message' else 'assistant', 'notification', body,
                     mirrored=True, attachments=attachments, content_tools=tools,
                     completion_suggested=payload.get('phase', payload.get('channel')) in ('final', 'final_answer'))
            elif typ == 'turn_context' or item in ('token_count', 'task_started', 'agent_reasoning', 'reasoning', 'compaction', 'context_compacted', 'turn_aborted'):
                emit(payload, line, kind='native_event', text=payload.get('text', ''), native_type=item or typ)
            else:
                preserve(payload, line, 'unsupported_codex_event', item or typ)
            continue
        if adapter == 'canonical' and typ in ('orbit.incomplete-native-evidence', 'orbit.redaction-limit'):
            emit(r, line, kind='native_event', text=r.get('text', ''), native_type=typ)
            # The notice is retained, but its producer could not preserve all
            # native bytes. Do not equate a readable notice with full coverage.
            issues.append({'line': line, 'reason': 'incomplete_source_evidence', 'retained': False})
        elif typ in ('task_complete', 'turn_end', 'final') or r.get('completion') == 'task_complete':
            emit(r, line, kind='completion', text=r.get('text', ''), completion_suggested=True, boundary=typ)
        elif isinstance(r.get('message'), dict):
            message(r, line, r['message'])
        elif 'role' in r:
            if r.get('kind') in ('tool_call', 'tool_result'):
                emit(r, line, r['role'], r['kind'], r.get('text', ''), tool_name=r.get('tool_name'), call_id=r.get('call_id'))
            else:
                message(r, line, r)
        elif typ in ('assistant', 'user', 'system') and isinstance(r.get('message'), str):
            message(r, line, {'role': typ, 'content': r['message']})
        elif typ in ('summary', 'custom', 'model_change', 'thinking_level_change', 'compaction', 'branch_summary', 'label', 'session_info', 'file-history-snapshot', 'queue-operation'):
            emit(r, line, kind='native_event', text=r.get('summary', r.get('text', '')), native_type=typ)
        else:
            preserve(r, line, 'unsupported_record', typ)
    return events, issues


def _read(path, adapter, max_bytes):
    # No-follow open and regular-file checks also reject FIFOs/devices.
    descriptor = os.open(path, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0) | getattr(os, 'O_NONBLOCK', 0))
    issues = []
    raw_hash = hashlib.sha256()
    total_bytes = record_count = 0
    with os.fdopen(descriptor, 'rb') as f:
        before = os.fstat(f.fileno())
        if not stat.S_ISREG(before.st_mode):
            raise Invalid('source_not_regular')
        if before.st_size > max_bytes:
            raise Invalid('source_byte_limit')
        def records():
            nonlocal total_bytes, record_count
            if path.suffix.lower() == '.json':
                raw = f.read(before.st_size)
                total_bytes = len(raw)
                if total_bytes > max_bytes:
                    raise Invalid('source_byte_limit')
                raw_hash.update(raw)
                try:
                    value = _json(raw)
                except ValueError:
                    issues.append({'line': 1, 'reason': 'malformed_json'})
                    return
                children = value.get('events', value.get('messages'))
                if isinstance(children, list):
                    for line, record in enumerate(children, 1):
                        if isinstance(record, dict):
                            record_count += 1
                            yield line, dict(record, session_id=record.get('session_id', value.get('session_id', value.get('id'))))
                        else:
                            issues.append({'line': line, 'reason': 'record_not_object'})
                else:
                    record_count += 1
                    yield 1, value
                return
            # Native session logs can contain hundreds of MB. Normalize each
            # line before reading the next; never retain raw + splitlines +
            # parsed copies of the whole source, including embedded media.
            line = 0
            while total_bytes < before.st_size:
                data = f.readline(before.st_size - total_bytes)
                if not data:
                    break
                line += 1
                total_bytes += len(data)
                if total_bytes > max_bytes:
                    raise Invalid('source_byte_limit')
                raw_hash.update(data)
                if not data.strip():
                    continue
                try:
                    value = _json(data)
                except ValueError:
                    issues.append({'line': line, 'reason': 'partial_last_record' if not data.endswith(b'\n') else 'malformed_json'})
                    continue
                record_count += 1
                yield line, value
        events, unsupported = _normalize(records(), adapter, 'generated_session_' + hashlib.sha256(str(path).encode()).hexdigest()[:24])
        after = os.fstat(f.fileno())
        if total_bytes != before.st_size or after.st_size < before.st_size:
            raise Invalid('source_changed_retryable')
        if (before.st_ino, before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (after.st_ino, after.st_size, after.st_mtime_ns, after.st_ctime_ns):
            # Active transcripts append while parsing. Accept the exact initial
            # byte prefix only after independently proving it was not rewritten.
            f.seek(0)
            remaining = before.st_size
            verification = hashlib.sha256()
            while remaining:
                block = f.read(min(1_048_576, remaining))
                if not block:
                    raise Invalid('source_changed_retryable')
                verification.update(block)
                remaining -= len(block)
            if verification.digest() != raw_hash.digest() or os.fstat(f.fileno()).st_size < before.st_size:
                raise Invalid('source_changed_retryable')
    issues.extend(unsupported)
    blocking = sum(not issue.get('retained', False) for issue in issues)
    return events, {'status': 'partial' if blocking else 'complete', 'issues': issues[:200],
                    'issue_count': len(issues), 'blocking_issue_count': blocking,
                    'semantic_issue_count': len(issues) - blocking,
                    'record_count': record_count, 'event_count': len(events),
                    'normalizer_version': NORMALIZER_VERSION,
                    'source_mtime': before.st_mtime, 'snapshot_bytes': before.st_size,
                    'append_pending': after.st_size > before.st_size,
                    'raw_sha256': raw_hash.hexdigest(), 'bytes': total_bytes}


def _timestamp(value):
    if type(value) in (int, float):
        result = value / 1000 if value > 100_000_000_000 else value
        return result if math.isfinite(result) and result > 0 else None
    if isinstance(value, str):
        try:
            parsed = datetime.fromisoformat(value.replace('Z', '+00:00'))
            return parsed.replace(tzinfo=timezone.utc).timestamp() if parsed.tzinfo is None else parsed.timestamp()
        except (ValueError, OverflowError):
            pass
    return None


def session_activity(events, fallback=None):
    """Original event activity in epoch seconds, never the ingestion timestamp."""
    latest = None
    for event in events:
        raw = event.get('raw_record') or {}
        value = _timestamp(event.get('timestamp')) or _timestamp(raw.get('timestamp'))
        if value is not None:
            latest = max(latest or value, value)
    return latest if latest is not None else (_timestamp(fallback) or 0)


def session_title(events, fallback='Conversación'):
    """Use the first human request, excluding application/environment envelopes."""
    tags = r'environment_context|user_instructions|external_codex_apps_open_page|codex_apps_open_page|app_context|app-context|ide_context|turn_context'
    for event in events:
        if event.get('role') != 'user' or not isinstance(event.get('text'), str):
            continue
        text = event['text'].strip()
        request = re.search(r'(?im)^\s*#{0,6}\s*(?:My request|Mi solicitud|Mi petición)\s*:\s*', text)
        if request:
            text = text[request.end():]
        text = re.sub(r'<(' + tags + r')\b[^>]*>[\s\S]*?</\1\s*>', '', text, flags=re.I)
        text = re.sub(r'<(?:' + tags + r')\b[^>]*/>', '', text, flags=re.I).strip()
        if not text or re.match(r'(?i)^\s*(?:#\s*AGENTS\.md instructions|#\s*Files mentioned by the user|<environment_context\b|<user_instructions\b|<external_codex_apps_open_page\b)', text):
            continue
        text = re.sub(r'^\s*#+\s*', '', text)
        text = re.sub(r'\s+', ' ', text).strip()
        if text:
            return clean(text)[:150]
    return clean(fallback)[:150]


def _display_coverage(events, coverage, source_mtime=None):
    activity = session_activity(events)
    return {**coverage, 'activity_at': activity or session_activity([], source_mtime),
            'activity_basis': 'event_timestamp' if activity else 'source_mtime',
            'display_metadata_version': DISPLAY_METADATA_VERSION}


def refresh_session_metadata(store, limit=25, max_seconds=5):
    """Repair display fields using saved evidence; never change revisions/labels."""
    end = time.monotonic() + max_seconds
    changed = 0
    for session in store.rows('SELECT * FROM sessions ORDER BY updated DESC'):
        if changed >= limit or time.monotonic() >= end:
            break
        coverage = json.loads(session['coverage'])
        if coverage.get('display_metadata_version') == DISPLAY_METADATA_VERSION:
            continue
        revision = store.one('SELECT events FROM revisions WHERE id=?', (session['revision'],))
        if not revision:
            continue
        events = json.loads(revision['events'])
        try: mtime = Path(session['path']).stat().st_mtime
        except OSError: mtime = coverage.get('source_mtime')
        coverage = _display_coverage(events, coverage, mtime)
        store.execute('UPDATE sessions SET title=?,coverage=? WHERE id=? AND revision=?',
                      (session_title(events, Path(session['path']).name), js(coverage), session['id'], session['revision']))
        changed += 1
    return {'updated': changed}


def _recent_paths(source, deadline, limit):
    """Bounded discovery by directory recency, then rank files by actual mtime."""
    root = Path(source['path'])
    if root.is_file() or root.is_symlink():
        return []
    directories = [(0, str(root))]
    files = []
    visited = 0
    end = min(deadline, time.monotonic() + .5)
    while directories and visited < max(500, limit * 200) and time.monotonic() < end:
        _, name = heapq.heappop(directories)
        directory = Path(name)
        if directory.is_symlink():
            continue
        try:
            with os.scandir(directory) as entries:
                for entry in entries:
                    visited += 1
                    if visited > max(500, limit * 200) or time.monotonic() >= end:
                        break
                    if entry.is_symlink():
                        continue
                    if entry.is_dir(follow_symlinks=False):
                        if entry.name not in ('.git', 'node_modules', '__pycache__', '.venv'):
                            heapq.heappush(directories, (-entry.stat(follow_symlinks=False).st_mtime_ns, entry.path))
                    elif entry.is_file(follow_symlinks=False) and Path(entry.name).suffix.lower() in EXTENSIONS and entry.name.lower() not in EXCLUDED:
                        files.append((entry.stat(follow_symlinks=False).st_mtime_ns, entry.path))
        except OSError:
            continue
    return [Path(path) for _, path in sorted(files, reverse=True)[:limit]]


def _scan_paths(store, source, deadline, limit):
    key = 'capture_priority_cycle:' + source['id']
    cycle = store.get(key, 0)
    store.set(key, cycle + 1)
    seen = set()
    # Alternate priority scans with traversal-only scans: even continuously
    # changing large files cannot starve older evidence or newly visited trees.
    if cycle % 2 == 0:
        recent = _recent_paths(source, deadline, max(1, min(10, limit // 4)))
        cursor_key = 'capture_recent_attempts:' + source['id']
        progress = store.get(cursor_key, {'sequence': 0, 'paths': {}})
        # File mtimes keep changing and reorder the recent list. Fairness is
        # attached to stable path identities, never to positions in that list.
        def priority(item):
            index, path = item
            previous = progress['paths'].get(digest(str(path)))
            if previous is not None:
                return (1, previous, index)
            mark = store.one('SELECT seen FROM file_marks WHERE path=?', (source['id'] + ':' + str(path),))
            # Existing capture timestamps seed a fresh scheduler, so an active
            # chat last captured long ago precedes recently refreshed agents.
            return (0, mark['seen'] if mark else 0, index)
        recent = sorted(enumerate(recent), key=priority)
        for _, path in recent:
            # Parsing one large active transcript can exhaust the whole scan.
            # Advance before yielding so the next priority scan starts with
            # another recent file even if this generator is closed immediately.
            progress['sequence'] += 1
            progress['paths'][digest(str(path))] = progress['sequence']
            if len(progress['paths']) > 512:
                progress['paths'] = dict(sorted(progress['paths'].items(), key=lambda item: item[1], reverse=True)[:256])
            store.set(cursor_key, progress)
            seen.add(str(path))
            yield path
    for path in _next_paths(store, source, deadline, limit):
        if str(path) not in seen:
            yield path


def _next_paths(store, source, deadline, limit):
    root = Path(source['path'])
    if not root.exists() or root.is_symlink():
        raise Invalid('source_unavailable')
    if root.is_file():
        yield root
        return
    key = 'capture_cursor:' + source['id']
    cursor = store.get(key, {'queue': [''], 'offset': 0})
    queue = cursor.get('queue') or ['']
    offset = cursor.get('offset', 0)
    visited = 0
    # Persist a breadth-first traversal queue and directory-entry offset. A scan
    # resumes rather than repeatedly examining only the first N files.
    while queue and visited < limit * 20 and time.monotonic() < deadline:
        relative = queue[0]
        directory = root / relative
        if directory.is_symlink() or not directory.is_dir():
            queue.pop(0); offset = 0; continue
        exhausted = True
        with os.scandir(directory) as entries:
            for index, entry in enumerate(entries):
                if index < offset:
                    continue
                if visited >= limit * 20 or time.monotonic() >= deadline:
                    exhausted = False; break
                offset = index + 1
                visited += 1
                if entry.is_symlink():
                    continue
                if entry.is_dir(follow_symlinks=False):
                    if entry.name not in ('.git', 'node_modules', '__pycache__', '.venv'):
                        queue.append(str(Path(relative) / entry.name))
                elif entry.is_file(follow_symlinks=False) and Path(entry.name).suffix.lower() in EXTENSIONS and entry.name.lower() not in EXCLUDED:
                    store.set(key, {'queue': queue, 'offset': offset})
                    yield Path(entry.path)
        if exhausted:
            queue.pop(0); offset = 0
        store.set(key, {'queue': queue, 'offset': offset})
    if not queue:
        store.set(key, {'queue': [''], 'offset': 0})


def _auto_episodes(store, session_id, revision, events):
    start = 0
    # Prefer native task boundaries over final messages when both are present.
    explicit = any(e.get('boundary') in ('task_complete', 'turn_end') for e in events)
    for index, event in enumerate(events):
        boundary = event.get('boundary') in ('task_complete', 'turn_end') if explicit else event.get('completion_suggested')
        if not boundary:
            continue
        eid = 'episode_' + digest([session_id, events[start:index + 1]])[:24]
        old = store.one('SELECT * FROM episodes WHERE id=?', (eid,))
        if not old:
            title = session_title(events[start:index + 1], 'Tarea detectada')
            store.execute('INSERT INTO episodes VALUES (?,?,?,?,?,?,?,?,?,?)',
                          (eid, session_id, revision, title, start, index, 'pending', '', '[]', now()))
        start = index + 1


def _capture(store, source, path, max_bytes):
    registered = Path(source['path'])
    allowed_root = registered if registered.is_dir() else registered.parent
    if path.is_symlink() or not path.resolve().is_relative_to(allowed_root.resolve()):
        raise Invalid('source_outside_registered_root')
    st = path.stat(follow_symlinks=False)
    if not stat.S_ISREG(st.st_mode):
        raise Invalid('source_not_regular')
    fingerprint = digest([NORMALIZER_VERSION, source['adapter'], st.st_dev, st.st_ino, st.st_size, st.st_mtime_ns, st.st_ctime_ns])
    key = source['id'] + ':' + str(path)
    mark = store.one('SELECT * FROM file_marks WHERE path=?', (key,))
    if mark and mark['fingerprint'] == fingerprint:
        return 0
    events, coverage = _read(path, source['adapter'], max_bytes)
    coverage = _display_coverage(events, coverage, coverage.get('source_mtime', st.st_mtime))
    # One file is one lineage; native session IDs remain on every event. This
    # avoids silently replacing one source's evidence with a same-ID copy.
    existing = store.one('SELECT id FROM sessions WHERE source_id=? AND path=?', (source['id'], str(path)))
    session_id = existing['id'] if existing else 'session_' + digest([source['id'], str(path.relative_to(Path(source['path'])) if Path(source['path']).is_dir() else path.name)])[:24]
    revision = 'revision_' + digest([session_id, events, coverage])
    title = session_title(events, path.name)
    completion = 'suggested' if any(e.get('completion_suggested') for e in events) else 'unknown'
    with store.transaction():
        store.execute('INSERT OR IGNORE INTO revisions VALUES (?,?,?,?)', (revision, session_id, js(events), now()))
        store.set('revision_coverage:' + revision, coverage)
        store.execute('INSERT OR REPLACE INTO sessions VALUES (?,?,?,?,?,?,?,?)',
                      (session_id, source['id'], str(path), clean(title), revision, now(), completion, js(coverage)))
        store.execute('INSERT OR REPLACE INTO file_marks VALUES (?,?,?)', (key, fingerprint, now()))
        if not store.get('config').get('auto_tasks'):
            _auto_episodes(store, session_id, revision, events)
    return 1


def scan_sources(store):
    config = store.get('config', {})
    summary = {'sources': 0, 'files': 0, 'changed': 0, 'sessions': 0, 'errors': 0, 'issues': [], 'limited': False}
    if not config.get('capture', True):
        summary['disabled'] = True
        return summary
    cap = max(1, min(100000, int(config.get('max_files_per_scan', 200))))
    deadline = time.monotonic() + max(.01, min(300, float(config.get('max_scan_seconds', 10))))
    summary['metadata_refresh'] = refresh_session_metadata(store, limit=5, max_seconds=max(.01, min(1, (deadline-time.monotonic()) / 10)))
    max_bytes = max(1, min(512_000_000, int(config.get('max_source_bytes', 64_000_000))))
    sources = store.rows('SELECT * FROM sources WHERE enabled=1 ORDER BY created,id')
    if not sources:
        return summary
    start = store.get('capture_source_cursor', 0) % len(sources)
    sources = sources[start:] + sources[:start]
    for index, source in enumerate(sources):
        if summary['files'] >= cap or time.monotonic() >= deadline:
            summary['limited'] = True; break
        summary['sources'] += 1
        error = None
        try:
            for path in _scan_paths(store, source, deadline, cap):
                summary['files'] += 1
                attempt_key = 'capture_attempt:' + source['id']
                attempt = {'source_id': source['id'], 'path': str(path), 'started': now(), 'status': 'running'}
                store.set(attempt_key, attempt)
                try:
                    changed = _capture(store, source, path, max_bytes)
                    summary['changed'] += changed
                    store.set(attempt_key, {**attempt, 'finished': now(), 'status': 'captured' if changed else 'unchanged'})
                except (Invalid, OSError, ValueError, RecursionError) as exc:
                    reason = str(exc) if isinstance(exc, Invalid) else 'unreadable_source_file'
                    error = reason
                    summary['errors'] += 1
                    summary['issues'].append({'source_id': source['id'], 'reason': reason, 'file_name': clean(path.name)})
                    store.set(attempt_key, {**attempt, 'finished': now(), 'status': 'failed', 'reason': reason})
                if summary['files'] >= cap or time.monotonic() >= deadline:
                    summary['limited'] = True; break
        except (OSError, Invalid):
            error = 'source_unavailable'
            summary['errors'] += 1
            summary['issues'].append({'source_id': source['id'], 'reason': error})
        store.execute('UPDATE sources SET error=? WHERE id=?', (error, source['id']))
        store.set('capture_source_cursor', (start + index + 1) % len(sources))
    summary['sessions'] = summary['changed']
    return summary


def session_detail(store, id):
    session = store.one('SELECT * FROM sessions WHERE id=?', (id,))
    if not session:
        raise Invalid('Conversación no encontrada.')
    revision = store.one('SELECT * FROM revisions WHERE id=?', (session['revision'],))
    session['coverage'] = json.loads(session['coverage'])
    session['events'] = json.loads(revision['events']) if revision else []
    session['episodes'] = store.rows('SELECT * FROM episodes WHERE session_id=? ORDER BY created', (id,))
    session['revisions'] = store.rows('SELECT id,created FROM revisions WHERE session_id=? ORDER BY created', (id,))
    return session


def create_episode(store, session_id, first_event=0, last_event=None, title=None):
    session = session_detail(store, session_id)
    events = session['events']
    if last_event is None:
        last_event = len(events) - 1
    if type(first_event) is not int or type(last_event) is not int or not 0 <= first_event <= last_event < len(events):
        raise Invalid('Selecciona un rango válido de eventos (índices desde cero, final incluido).')
    title = clean(title if title else session['title'])[:240]
    id = uid('episode')
    store.execute('INSERT INTO episodes VALUES (?,?,?,?,?,?,?,?,?,?)',
                  (id, session_id, session['revision'], title, first_event, last_event, 'pending', '', '[]', now()))
    return store.one('SELECT * FROM episodes WHERE id=?', (id,))
