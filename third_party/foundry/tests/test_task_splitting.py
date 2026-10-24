import json
import tempfile
import unittest
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

from foundry_loop.common import Invalid, js, now
from foundry_loop.episodes import create_episode_safe, session_detail_revision
from foundry_loop.store import Store


class TaskSplittingTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.store = Store(Path(self.temp.name) / 'state')
        self.events = [
            {'id': 'u1', 'sequence': 0, 'role': 'user', 'text': 'First independent task'},
            {'id': 'a1', 'sequence': 1, 'role': 'assistant', 'text': 'First result', 'completion_suggested': True},
            {'id': 'u2', 'sequence': 2, 'role': 'user', 'text': 'Second independent task'},
            {'id': 'a2', 'sequence': 3, 'role': 'assistant', 'text': 'Second result', 'completion_suggested': True},
        ]
        self.revision('session_a', 'revision_a', self.events)

    def tearDown(self):
        self.store.db.close()
        self.temp.cleanup()

    def revision(self, session, revision, events, coverage=None):
        coverage = coverage or {'status': 'complete', 'event_count': len(events)}
        self.store.execute('INSERT INTO revisions VALUES (?,?,?,?)', (revision, session, js(events), now()))
        self.store.set('revision_coverage:' + revision, coverage)
        self.store.execute('INSERT OR REPLACE INTO sessions VALUES (?,?,?,?,?,?,?,?)',
                           (session, 'fixture-source', 'fixture.jsonl', 'Conversation', revision, now(), 'suggested', js(coverage)))

    def test_multiple_tasks_keep_ranges_on_one_immutable_revision(self):
        first = create_episode_safe(self.store, 'session_a', 0, 1, 'First', 'revision_a')
        second = create_episode_safe(self.store, 'session_a', 2, 3, 'Second', 'revision_a')
        self.assertNotEqual(first['id'], second['id'])
        self.assertEqual((first['first_event'], first['last_event']), (0, 1))
        self.assertEqual((second['first_event'], second['last_event']), (2, 3))
        self.assertEqual(first['revision'], second['revision'])
        self.assertEqual(first['label'], 'pending')
        self.assertEqual(second['label'], 'pending')

    def test_visible_revision_stays_pinned_after_new_capture(self):
        newer = [{'role': 'service', 'text': 'Inserted event'}, *self.events]
        self.revision('session_a', 'revision_b', newer)
        episode = create_episode_safe(self.store, 'session_a', 0, 1, expected_revision='revision_a')
        self.assertEqual(episode['revision'], 'revision_a')
        self.assertFalse(episode['snapshot_is_latest'])
        original = session_detail_revision(self.store, 'session_a', episode['revision'])
        self.assertEqual(original['events'][episode['first_event']]['id'], 'u1')
        self.assertEqual(original['events'][episode['last_event']]['id'], 'a1')
        self.assertEqual(original['latest_revision'], 'revision_b')
        self.assertFalse(original['is_latest'])

    def test_exact_range_retry_preserves_labels_feedback_cases_and_title(self):
        episode = create_episode_safe(self.store, 'session_a', 0, 1, 'User title', 'revision_a')
        self.store.execute('UPDATE episodes SET label=?,feedback=?,cases_json=? WHERE id=?',
                           ('correct', 'Human reviewed this revision', '["case_original"]', episode['id']))
        self.store.execute('INSERT INTO labels VALUES (?,?,?,?,?)',
                           ('label_original', episode['id'], 'correct', 'Human reviewed this revision', now()))
        retry = create_episode_safe(self.store, 'session_a', 0, 1, 'Different requested title', 'revision_a')
        self.assertEqual(retry['id'], episode['id'])
        self.assertTrue(retry['reused'])
        self.assertTrue(retry['title_preserved'])
        self.assertEqual(retry['title'], 'User title')
        self.assertEqual(retry['label'], 'correct')
        self.assertEqual(retry['feedback'], 'Human reviewed this revision')
        self.assertEqual(retry['cases_json'], '["case_original"]')
        self.assertEqual(len(self.store.rows('SELECT * FROM labels')), 1)
        self.assertEqual(len(self.store.rows('SELECT * FROM episodes')), 1)

    def test_concurrent_duplicate_requests_create_one_task(self):
        def create(_):
            return create_episode_safe(self.store, 'session_a', 0, 1, expected_revision='revision_a')['id']
        with ThreadPoolExecutor(max_workers=4) as executor:
            ids = list(executor.map(create, range(12)))
        self.assertEqual(len(set(ids)), 1)
        self.assertEqual(len(self.store.rows('SELECT * FROM episodes')), 1)

    def test_revision_owner_and_range_validation(self):
        self.revision('session_b', 'revision_foreign', self.events)
        with self.assertRaisesRegex(Invalid, 'pertenece'):
            create_episode_safe(self.store, 'session_a', 0, 1, expected_revision='revision_foreign')
        with self.assertRaisesRegex(Invalid, 'pertenece'):
            session_detail_revision(self.store, 'session_a', 'revision_foreign')
        for first, last in ((True, 1), (0, False), (-1, 1), (2, 1), (0, 4), (0.0, 1)):
            with self.subTest(first=first, last=last), self.assertRaises(Invalid):
                create_episode_safe(self.store, 'session_a', first, last)
        self.assertEqual(self.store.rows('SELECT * FROM episodes'), [])

    def test_detail_filters_tasks_and_uses_historical_coverage(self):
        original = create_episode_safe(self.store, 'session_a', 0, 1, expected_revision='revision_a')
        self.store.set('revision_coverage:revision_a', {'status': 'partial', 'reason': 'fixture'})
        self.revision('session_a', 'revision_b', self.events + [{'role': 'user', 'text': 'Later'}])
        latest = create_episode_safe(self.store, 'session_a', 2, 3)
        detail = session_detail_revision(self.store, 'session_a', 'revision_a')
        self.assertEqual([episode['id'] for episode in detail['episodes']], [original['id']])
        self.assertEqual(detail['coverage']['status'], 'partial')
        self.assertEqual(detail['historical_episode_count'], 1)
        self.assertEqual({revision['id']: revision['episode_count'] for revision in detail['revisions']},
                         {'revision_a': 1, 'revision_b': 1})
        self.assertEqual(latest['revision'], 'revision_b')

    def test_missing_historical_coverage_is_not_borrowed_from_latest(self):
        self.store.execute('DELETE FROM kv WHERE key=?', ('revision_coverage:revision_a',))
        self.revision('session_a', 'revision_b', self.events)
        detail = session_detail_revision(self.store, 'session_a', 'revision_a')
        self.assertEqual(detail['coverage']['status'], 'unknown')

    def test_compact_projection_keeps_indices_without_mutating_evidence(self):
        events = [dict(self.events[0], text='x' * 6000, raw_record={'large': 'y' * 10000},
                       attachments=[{'kind': 'unsupported_content', 'raw_content': {'large': 'z' * 10000}, 'name': 'file.txt'}]),
                  self.events[1]]
        self.revision('session_a', 'revision_b', events)
        before = self.store.one('SELECT events FROM revisions WHERE id=?', ('revision_b',))['events']
        compact = session_detail_revision(self.store, 'session_a', compact=True)
        self.assertEqual(compact['event_count'], 2)
        self.assertEqual([event['id'] for event in compact['events']], ['u1', 'a1'])
        self.assertEqual(len(compact['events'][0]['text']), 4000)
        self.assertTrue(compact['events'][0]['event_text_truncated'])
        self.assertFalse(compact['events'][1]['event_text_truncated'])
        self.assertNotIn('raw_record', json.dumps(compact['events']))
        self.assertNotIn('raw_content', json.dumps(compact['events']))
        self.assertEqual(compact['events'][0]['attachments'][0]['name'], 'file.txt')
        after = self.store.one('SELECT events FROM revisions WHERE id=?', ('revision_b',))['events']
        self.assertEqual(before, after)
        full = session_detail_revision(self.store, 'session_a')
        self.assertEqual(len(full['events'][0]['text']), 6000)
        self.assertEqual(full['events'][0]['raw_record'], events[0]['raw_record'])

    def test_http_pinned_create_after_append_and_historical_task_inspection(self):
        import subprocess
        import sys
        from urllib.error import HTTPError
        from urllib.request import Request, urlopen
        config = self.store.get('config')
        config.update(capture=False, auto_derive=False)
        self.store.set('config', config)
        token = self.store.get('token')
        code = 'import json,sys;from foundry_loop.server import serve;serve(sys.argv[1],0,lambda m:print(json.dumps(m),flush=True))'
        child = subprocess.Popen([sys.executable, '-u', '-c', code, str(self.store.root)],
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        base = None
        try:
            metadata = json.loads(child.stdout.readline())
            base = 'http://127.0.0.1:' + str(metadata['port'])
            def call(path, body=None):
                headers = {'X-Foundry-Token': token}
                if body is not None:
                    headers['Content-Type'] = 'application/json'
                request = Request(base + path, data=js(body).encode() if body is not None else None, headers=headers)
                try:
                    with urlopen(request, timeout=5) as response:
                        return response.status, json.load(response)
                except HTTPError as error:
                    try:
                        return error.code, json.load(error)
                    finally:
                        error.close()
            status, visible = call('/api/session?id=session_a')
            self.assertEqual(status, 200)
            self.assertEqual(visible['revision'], 'revision_a')
            self.assertTrue(visible['events_compact'])
            appended = self.events + [
                {'role': 'user', 'text': 'z' * 5000, 'raw_record': {'native': 'fixture evidence'}},
                {'role': 'assistant', 'text': 'Third result'},
            ]
            self.revision('session_a', 'revision_b', appended)
            status, created = call('/api/action', {'action': 'episode.create', 'session_id': 'session_a',
                                                  'first_event': 0, 'last_event': 1, 'title': 'Reviewed task',
                                                  'expected_revision': visible['revision']})
            self.assertEqual(status, 200)
            self.assertEqual(created['revision'], 'revision_a')
            self.assertFalse(created['snapshot_is_latest'])
            self.assertEqual(call('/api/action', {'action': 'episode.complete', 'id': created['id'],
                                                 'completed': True})[0], 200)
            self.assertEqual(call('/api/action', {'action': 'episode.label', 'id': created['id'],
                                                 'label': 'correct', 'feedback': 'Reviewed old snapshot'})[0], 200)
            status, reused = call('/api/action', {'action': 'episode.create', 'session_id': 'session_a',
                                                 'first_event': 0, 'last_event': 1, 'title': 'New title ignored',
                                                 'expected_revision': 'revision_a'})
            self.assertEqual(status, 200)
            self.assertTrue(reused['reused'])
            self.assertEqual(reused['id'], created['id'])
            self.assertEqual(reused['label'], 'correct')
            status, latest_task = call('/api/action', {'action': 'episode.create', 'session_id': 'session_a',
                                                      'first_event': 4, 'last_event': 5, 'title': 'Latest task'})
            self.assertEqual(status, 200)
            self.assertEqual(latest_task['revision'], 'revision_b')
            status, all_tasks = call('/api/episodes?session_id=session_a')
            self.assertEqual(status, 200)
            self.assertEqual({task['id'] for task in all_tasks['episodes']}, {created['id'], latest_task['id']})
            self.assertTrue(all(isinstance(task['cases'], list) and 'cases_json' not in task for task in all_tasks['episodes']))
            status, historical = call('/api/session?id=session_a&revision=revision_a')
            self.assertEqual(status, 200)
            self.assertEqual(historical['event_count'], 4)
            self.assertEqual(historical['latest_revision'], 'revision_b')
            self.assertEqual([task['id'] for task in historical['episodes']], [created['id']])
            status, latest = call('/api/session?id=session_a')
            self.assertEqual(status, 200)
            self.assertEqual(latest['event_count'], 6)
            self.assertEqual(len(latest['events'][4]['text']), 4000)
            self.assertTrue(latest['events'][4]['event_text_truncated'])
            self.assertNotIn('raw_record', latest['events'][4])
            status, full = call('/api/session?id=session_a&revision=revision_b&compact=0')
            self.assertEqual(status, 200)
            self.assertEqual(len(full['events'][4]['text']), 5000)
            self.assertEqual(full['events'][4]['raw_record'], {'native': 'fixture evidence'})
            self.assertEqual(call('/api/episodes?session_id=missing')[0], 400)
            self.assertEqual(call('/api/action', {'action': 'shutdown'})[0], 200)
            child.wait(timeout=7)
            self.assertEqual(child.returncode, 0)
        finally:
            if child.poll() is None:
                child.kill()
                child.wait()
            child.stdout.close()
            child.stderr.close()


if __name__ == '__main__':
    unittest.main()
