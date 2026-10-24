"""Create task slices against the exact immutable transcript a user reviewed.

``expected_revision`` means the visible snapshot, not a compare-and-swap guard
against the latest capture. A new capture must never reinterpret old indices.
"""
from __future__ import annotations

import json

from .common import Invalid, clean, now, uid


def _selected_revision(store, session_id, revision=None):
    if not isinstance(session_id, str) or not session_id:
        raise Invalid('Indica una conversación válida.')
    if revision is not None and (not isinstance(revision, str) or not revision):
        raise Invalid('Indica una revisión válida.')
    session = store.one('SELECT * FROM sessions WHERE id=?', (session_id,))
    if not session:
        raise Invalid('Conversación no encontrada.')
    selected = revision if revision is not None else session['revision']
    snapshot = store.one('SELECT * FROM revisions WHERE id=? AND session_id=?', (selected, session_id))
    if not snapshot:
        raise Invalid('La revisión no pertenece a esta conversación o ya no está disponible.')
    try:
        events = json.loads(snapshot['events'])
    except (ValueError, TypeError):
        raise Invalid('La revisión almacenada no contiene eventos válidos.') from None
    if not isinstance(events, list) or any(not isinstance(event, dict) for event in events):
        raise Invalid('La revisión almacenada no contiene una lista de eventos.')
    return session, snapshot, events


def session_detail_revision(store, session_id, revision=None, compact=False):
    """Return events, coverage and tasks from one session-owned revision.

    Historical tasks remain available through ``revisions`` and the global task
    list. They are not displayed against a different revision's event indices.
    """
    if type(compact) is not bool:
        raise Invalid('compact debe ser booleano.')
    session, snapshot, events = _selected_revision(store, session_id, revision)
    selected = snapshot['id']
    coverage = store.get('revision_coverage:' + selected)
    if not isinstance(coverage, dict):
        if selected == session['revision']:
            try:
                coverage = json.loads(session['coverage'])
            except (TypeError, ValueError):
                coverage = None
        if not isinstance(coverage, dict):
            coverage = {'status': 'unknown', 'reason': 'revision_coverage_unavailable',
                        'event_count': len(events)}
    result = dict(session)
    result.update(revision=selected, latest_revision=session['revision'],
                  is_latest=selected == session['revision'], revision_created=snapshot['created'],
                  coverage=coverage, events=events, event_count=len(events), events_compact=compact)
    result['completion'] = 'suggested' if any(event.get('completion_suggested') for event in events) else 'unknown'
    result['episodes'] = store.rows(
        'SELECT * FROM episodes WHERE session_id=? AND revision=? ORDER BY first_event,last_event,created,id',
        (session_id, selected))
    result['episodes'] = [store.enrich_episode(episode) for episode in result['episodes']]
    result['episodes'] = [episode for episode in result['episodes'] if not episode['superseded']]
    result['task_proposal'] = store.get('task_proposals:'+session_id+':'+selected)
    result['revisions'] = store.rows(
        'SELECT r.id,r.created,COUNT(e.id) AS episode_count FROM revisions r '
        'LEFT JOIN episodes e ON e.revision=r.id AND e.session_id=r.session_id '
        'WHERE r.session_id=? GROUP BY r.id,r.created ORDER BY r.created,r.id', (session_id,))
    count = store.one('SELECT COUNT(*) AS total FROM episodes WHERE session_id=? AND revision<>?',
                      (session_id, selected))
    result['historical_episode_count'] = count['total']
    if compact:
        # Presentation only: retain every array position and leave the immutable
        # evidence in revisions untouched. Never pass this projection to derive.
        allowed = ('id', 'sequence', 'role', 'kind', 'channel', 'timestamp',
                   'completion_suggested', 'tool_name', 'call_id', 'line')
        attachment_keys = ('id', 'kind', 'type', 'mime_type', 'media_type', 'name',
                           'filename', 'path', 'url', 'image_url', 'inert', 'descriptor_only')
        projected = []
        for event in events:
            item = {key: event[key] for key in allowed if key in event}
            original_text = event.get('text', '')
            original_text = original_text if isinstance(original_text, str) else ''
            item['text'] = original_text[:4000]
            item['event_text_truncated'] = len(original_text) > 4000
            attachments = event.get('attachments', [])
            if isinstance(attachments, list):
                item['attachment_count'] = len(attachments)
                item['attachments'] = []
                for attachment in attachments[:100]:
                    if not isinstance(attachment, dict):
                        continue
                    descriptor = {}
                    for key in attachment_keys:
                        value = attachment.get(key)
                        if isinstance(value, str):
                            descriptor[key] = value[:1000]
                        elif value is None or type(value) in (bool, int, float):
                            if key in attachment:
                                descriptor[key] = value
                    item['attachments'].append(descriptor)
            projected.append(item)
        result['events'] = projected
        result['event_text_limit'] = 4000
    return result


def create_episode_safe(store, session_id, first_event=0, last_event=None, title=None, expected_revision=None):
    """Create or reuse an exact slice; bounds are zero-based and inclusive.

    ``expected_revision`` pins the immutable snapshot currently visible to the
    user, including a historical revision. Omission preserves the old API's
    latest-revision behavior. Existing labels, feedback, cases and titles never
    change when the same range is submitted again, including concurrent calls.
    """
    if title is not None and not isinstance(title, str):
        raise Invalid('El título debe ser texto.')
    with store.transaction():
        session, snapshot, events = _selected_revision(store, session_id, expected_revision)
        selected = snapshot['id']
        if last_event is None:
            last_event = len(events) - 1
        if type(first_event) is not int or type(last_event) is not int or not 0 <= first_event <= last_event < len(events):
            raise Invalid('Selecciona un rango válido de eventos (índices desde cero, final incluido).')
        requested_title = clean(title).strip()[:240] if title is not None else ''
        existing = store.one(
            'SELECT * FROM episodes WHERE session_id=? AND revision=? AND first_event=? AND last_event=? '
            "ORDER BY CASE WHEN label='pending' THEN 1 ELSE 0 END,created,id LIMIT 1",
            (session_id, selected, first_event, last_event))
        if existing:
            result = store.enrich_episode(existing)
            result.update(reused=True, snapshot_is_latest=selected == session['revision'],
                          title_preserved=bool(requested_title and requested_title != existing['title']))
            return result
        inferred_title = next((event['text'] for event in events[first_event:last_event + 1]
                               if event.get('role') == 'user' and isinstance(event.get('text'), str)
                               and event['text'].strip()), session['title'])
        chosen_title = requested_title or clean(inferred_title).strip()[:240] or 'Tarea seleccionada'
        episode_id = uid('episode')
        store.execute('INSERT INTO episodes VALUES (?,?,?,?,?,?,?,?,?,?)',
                      (episode_id, session_id, selected, chosen_title, first_event, last_event,
                       'pending', '', '[]', now()))
        store.audit('episode_created', {'id': episode_id, 'session_id': session_id, 'revision': selected,
                                       'first_event': first_event, 'last_event': last_event, 'source': 'manual'})
        result = store.enrich_episode(store.one('SELECT * FROM episodes WHERE id=?', (episode_id,)))
        result.update(reused=False, snapshot_is_latest=selected == session['revision'], title_preserved=False)
        return result
