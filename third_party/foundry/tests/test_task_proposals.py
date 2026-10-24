import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from foundry_loop.common import Invalid, js, now
from foundry_loop.episodes import create_episode_safe
from foundry_loop.ingest import _auto_episodes
from foundry_loop.store import Store
from foundry_loop.task_proposals import propose_tasks, task_proposal_status, SCHEMA


def task(first=0, last=1, quote='Build the API'):
    return {'title': 'Build API', 'first_event': first, 'last_event': last,
            'evidence': [{'event_index': first, 'quote': quote}]}


class TaskProposalTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(); self.store = Store(Path(self.tmp.name) / 'state')
        self.events = [{'role': 'user', 'text': 'Build the API'}, {'role': 'assistant', 'text': 'Implemented', 'completion_suggested': True}]
        self.revision('r1', self.events)

    def tearDown(self):
        self.store.db.close(); self.tmp.cleanup()

    def revision(self, revision, events, status='complete'):
        self.store.execute('INSERT INTO revisions VALUES (?,?,?,?)', (revision, 'session', js(events), now()))
        self.store.set('revision_coverage:' + revision, {'status': status})
        self.store.execute('INSERT OR REPLACE INTO sessions VALUES (?,?,?,?,?,?,?,?)',
                           ('session', 'source', '/fixture', 'Session', revision, now(), 'suggested', js({'status': status})))

    def response(self, tasks):
        return {'text': js({'tasks': tasks}), 'provider': 'command', 'model': 'mechanical',
                'usage': {'input_tokens': 10, 'output_tokens': 5}, 'real_inference': False, 'seconds': .01}

    def test_direct_creates_pending_only_and_unchanged_receipt_prevents_repeated_calls(self):
        with patch('foundry_loop.task_proposals.run_agent', return_value=self.response([task()])) as model:
            result = propose_tasks(self.store, 'session')
            second = propose_tasks(self.store, 'session')
        self.assertEqual(model.call_count, 1)
        self.assertTrue(second['reused']); self.assertEqual(result['status'], 'completed')
        episode = self.store.one('SELECT * FROM episodes')
        self.assertEqual(episode['label'], 'pending'); self.assertFalse(self.store.task_completed(episode['id']))
        self.assertEqual(self.store.rows('SELECT * FROM labels'), [])
        self.assertEqual(result['inference']['runs'][0]['model'], 'mechanical')
        self.assertEqual(result['inference']['runs'][0]['usage']['input_tokens'], 10)

    def test_manual_task_and_previous_proposal_not_duplicated_after_recapture(self):
        manual = create_episode_safe(self.store, 'session')
        newer = self.events + [{'role': 'user', 'text': 'Write a guide'}, {'role': 'assistant', 'text': 'Guide'}]
        self.revision('r2', newer)
        with patch('foundry_loop.task_proposals.run_agent', return_value=self.response([task(), task(2, 3, 'Write a guide')])):
            result = propose_tasks(self.store, 'session')
        self.assertEqual(len(result['created']), 1); self.assertEqual(result['skipped'][0]['episode_ids'], [manual['id']])
        self.revision('r3', [{'role': 'service', 'text': 'Metadata inserted'}] + newer)
        with patch('foundry_loop.task_proposals.run_agent', return_value=self.response([task(1, 2), task(3, 4, 'Write a guide')])):
            result = propose_tasks(self.store, 'session')
        self.assertEqual(result['created'], []); self.assertEqual(len(result['skipped']), 2)
        self.assertEqual(len(self.store.rows('SELECT * FROM episodes')), 2)

    def test_heuristics_can_be_superseded_but_manual_same_range_is_preserved(self):
        _auto_episodes(self.store, 'session', 'r1', self.events)
        heuristic = self.store.one('SELECT * FROM episodes')
        with patch('foundry_loop.task_proposals.run_agent', return_value=self.response([task()])):
            result = propose_tasks(self.store, 'session')
        self.assertEqual(self.store.get('task_superseded:' + heuristic['id'])['by'], result['created'])
        self.assertEqual(len(self.store.rows('SELECT * FROM episodes')), 2)
        new_events = [{**event, 'timestamp': '2026-10-13T06:00:00Z'} for event in self.events]
        self.revision('r2', new_events)
        _auto_episodes(self.store, 'session', 'r2', new_events)
        new_heuristic = self.store.one("SELECT * FROM episodes WHERE revision='r2'")
        with patch('foundry_loop.task_proposals.run_agent', return_value=self.response([task()])):
            again = propose_tasks(self.store, 'session')
        self.assertEqual(again['created'], [])
        self.assertEqual(self.store.get('task_superseded:' + new_heuristic['id'])['by'], result['created'])

    def test_reviewed_heuristics_are_protected(self):
        _auto_episodes(self.store, 'session', 'r1', self.events)
        heuristic = self.store.one('SELECT * FROM episodes')
        self.store.execute("UPDATE episodes SET feedback='Human note' WHERE id=?", (heuristic['id'],))
        with patch('foundry_loop.task_proposals.run_agent', return_value=self.response([task()])):
            result = propose_tasks(self.store, 'session')
        self.assertEqual(result['created'], []); self.assertIsNone(self.store.get('task_superseded:' + heuristic['id']))

    def test_reopened_heuristic_preserves_human_decision(self):
        from foundry_loop.app import App
        _auto_episodes(self.store, 'session', 'r1', self.events)
        heuristic = self.store.one('SELECT * FROM episodes')
        app = App.__new__(App); app.store = self.store
        app.action({'action': 'episode.complete', 'id': heuristic['id'], 'completed': True})
        app.action({'action': 'episode.complete', 'id': heuristic['id'], 'completed': False})
        self.assertFalse(self.store.task_completed(heuristic['id']))
        with patch('foundry_loop.task_proposals.run_agent', return_value=self.response([task()])):
            result = propose_tasks(self.store, 'session')
        self.assertEqual(result['created'], [])
        self.assertEqual(result['skipped'][0]['episode_ids'], [heuristic['id']])
        self.assertIsNone(self.store.get('task_superseded:' + heuristic['id']))
        self.assertEqual(len(self.store.rows('SELECT * FROM episodes')), 1)

    def test_final_checkpoint_survives_atomic_write_failure(self):
        execute = self.store.execute
        def fail_insert(sql, args=()):
            if sql.startswith('INSERT INTO episodes'): raise Invalid('Fixture commit failure')
            return execute(sql, args)
        with patch('foundry_loop.task_proposals.run_agent', return_value=self.response([task()])) as model:
            with patch.object(self.store, 'execute', side_effect=fail_insert), self.assertRaises(Invalid):
                propose_tasks(self.store, 'session')
            self.assertEqual(self.store.rows('SELECT * FROM episodes'), [])
            result = propose_tasks(self.store, 'session')
        self.assertEqual(model.call_count, 1); self.assertEqual(len(result['created']), 1)

    def long_fixture(self):
        cfg = self.store.get('config'); cfg.update(max_input_chars=2600, max_calls=40); self.store.set('config', cfg)
        events = [{'role': 'user', 'text': 'Build the API ' + 'x' * 7000}, {'role': 'assistant', 'text': 'answer ' * 900}]
        self.revision('long', events)
        return events

    def chunk_driver(self, config, prompt, cwd, *, cancel=None, schema=None):
        self.assertLessEqual(len(prompt), self.store.get('config')['max_input_chars'])
        self.assertNotEqual(str(cwd), str(self.store.root))
        data = json.loads(prompt.split('\nEVIDENCIA_JSON\n')[1])
        if schema == SCHEMA: return self.response([task()])
        observations = []
        for fragment in data['fragments']:
            if fragment['part'] == 'text' and fragment['event_index'] == 0 and fragment['start'] == 0:
                observations.append({'observation': 'User asks to build the API.',
                                     'evidence': [{'event_index': 0, 'quote': 'Build the API'}]})
        return {'text': js({'observations': observations}), 'provider': 'command', 'model': 'mechanical', 'real_inference': False}

    def test_long_input_covers_every_original_character_and_keeps_global_indices(self):
        events = self.long_fixture()
        with patch('foundry_loop.task_proposals.run_agent', side_effect=self.chunk_driver):
            result = propose_tasks(self.store, 'session')
        self.assertEqual(result['coverage']['mode'], 'chunks')
        self.assertEqual(result['progress']['completed_chunks'], result['progress']['total_chunks'])
        for index, event in enumerate(events):
            for part, length in [('text', len(event['text'])), ('metadata', len(js({k: v for k, v in event.items() if k != 'text'})))]:
                fragments = [f for f in result['coverage']['fragments'] if f['event_index'] == index and f['part'] == part]
                cursor = 0
                for fragment in fragments:
                    self.assertEqual(fragment['start'], cursor); cursor = fragment['end']
                self.assertEqual(cursor, length)

    def test_retry_reuses_completed_chunks_and_terminal_failure_is_visible(self):
        self.long_fixture(); calls = []
        def fail_once(*args, **kwargs):
            calls.append(args[1])
            if len(calls) == 2: raise Invalid('Mechanical interruption')
            return self.chunk_driver(*args, **kwargs)
        with patch('foundry_loop.task_proposals.run_agent', side_effect=fail_once):
            with self.assertRaises(Invalid): propose_tasks(self.store, 'session')
            self.assertEqual(task_proposal_status(self.store, 'session')['status'], 'failed')
            result = propose_tasks(self.store, 'session')
        self.assertEqual(calls.count(calls[0]), 1)
        self.assertEqual(result['status'], 'completed')

    def test_cancel_stale_revision_and_provider_changes_prevent_artifacts(self):
        for kind in ('cancel', 'revision', 'provider'):
            with self.subTest(kind=kind):
                job = self.store.job('propose_tasks', 'session'); self.store.claim()
                def revoke(*args, **kwargs):
                    if kind == 'cancel': self.store.execute("UPDATE jobs SET status='cancelled' WHERE id=?", (job,))
                    elif kind == 'revision': self.store.execute("UPDATE sessions SET revision='different' WHERE id='session'")
                    else:
                        cfg = self.store.get('config'); cfg['provider']['model'] = 'changed'; self.store.set('config', cfg)
                    return self.response([task()])
                with patch('foundry_loop.task_proposals.run_agent', side_effect=revoke), self.assertRaises(Invalid):
                    propose_tasks(self.store, 'session', job)
                self.assertEqual(self.store.rows('SELECT * FROM episodes'), [])
                self.assertEqual(task_proposal_status(self.store, 'session', 'r1')['status'],
                                 {'cancel': 'cancelled', 'revision': 'stale', 'provider': 'failed'}[kind])
                self.store.execute("UPDATE sessions SET revision='r1' WHERE id='session'")
                self.store.execute("UPDATE jobs SET status='failed' WHERE id=?", (job,))

    def test_invalid_proposals_never_assign_labels_or_execute_transcript(self):
        invalid = [task(0, 99), task(1, 1, 'Implemented'), task(0, 1, 'invented quote'), {**task(), 'completed': True}]
        for value in invalid:
            with self.subTest(value=value), patch('foundry_loop.task_proposals.run_agent', return_value=self.response([value])):
                with self.assertRaises(Invalid): propose_tasks(self.store, 'session')
                self.assertEqual(self.store.rows('SELECT * FROM episodes'), [])

    def test_empty_partial_and_budget_exhaustion_do_not_call_provider(self):
        for revision, events, coverage in [('empty', [], 'complete'), ('partial', self.events, 'partial')]:
            self.revision(revision, events, coverage)
            with patch('foundry_loop.task_proposals.run_agent') as model, self.assertRaises(Invalid):
                propose_tasks(self.store, 'session')
            model.assert_not_called()
        self.long_fixture(); cfg = self.store.get('config'); cfg['max_calls'] = 2; self.store.set('config', cfg)
        with patch('foundry_loop.task_proposals.run_agent') as model, self.assertRaises(Invalid):
            propose_tasks(self.store, 'session')
        model.assert_not_called(); self.assertEqual(task_proposal_status(self.store, 'session')['status'], 'failed')


if __name__ == '__main__': unittest.main()
