import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from foundry_loop.app import App, configure
from foundry_loop.common import Invalid, js, now
from foundry_loop.episodes import create_episode_safe, session_detail_revision
from foundry_loop.store import Store


class HumanGateTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.app = App(Path(self.tmp.name) / 'state')
        self.s = self.app.store
        self.s.execute('INSERT INTO revisions VALUES (?,?,?,?)', ('r', 's', js([
            {'role': 'user', 'text': 'Do the task'},
            {'role': 'assistant', 'text': 'Done', 'completion_suggested': True}]), now()))
        self.s.execute('INSERT INTO sessions VALUES (?,?,?,?,?,?,?,?)',
                       ('s', 'source', 'fixture', 'Task', 'r', now(), 'suggested', '{}'))
        self.id = create_episode_safe(self.s, 's')['id']

    def tearDown(self):
        self.s.db.close()
        self.tmp.cleanup()

    def complete(self, value=True):
        return self.app.action({'action': 'episode.complete', 'id': self.id, 'completed': value})

    def label(self, value='correct'):
        return self.app.action({'action': 'episode.label', 'id': self.id, 'label': value})

    def test_completion_and_correctness_are_independent(self):
        self.assertFalse(self.s.task_completed(self.id))
        for value in ('correct', 'incorrect'):
            with self.assertRaises(Invalid): self.label(value)
        with self.assertRaises(Invalid): self.app.action({'action': 'derive', 'id': self.id})
        self.label('pending')
        completed = self.complete()
        self.assertTrue(completed['completed'])
        self.assertEqual(completed['label'], 'pending')
        self.assertEqual(self.s.rows('SELECT * FROM jobs'), [])
        with self.assertRaises(Invalid): self.app.action({'action': 'derive', 'id': self.id})
        job = self.label('incorrect')
        self.assertTrue(job['job_id'])
        self.assertEqual(self.s.one('SELECT label FROM episodes WHERE id=?', (self.id,))['label'], 'incorrect')

    def test_endpoints_and_creation_enrich_without_inference(self):
        self.assertFalse(self.app.state()['episodes'][0]['completed'])
        detail = session_detail_revision(self.s, 's')
        self.assertEqual(detail['completion'], 'suggested')
        self.assertFalse(detail['episodes'][0]['completed'])
        done = self.complete()
        reused = create_episode_safe(self.s, 's')
        self.assertEqual(reused['completion_generation'], done['completion_generation'])
        self.assertEqual(self.app.state()['episodes'][0]['completion_confirmed_at'], done['completion_confirmed_at'])

    def test_reopen_cancels_work_preserves_history_and_is_idempotent(self):
        self.complete()
        derive_job = self.label()['job_id']
        self.s.execute('INSERT INTO candidates VALUES (?,?,?,?,?,?)', ('c', self.id, '{}', 'ready', 'initial', now()))
        self.s.execute('INSERT INTO candidates VALUES (?,?,?,?,?,?)', ('active', self.id, '{}', 'accepted', 'initial', now()))
        evaluation_job = self.s.job('evaluate', 'c')
        before = self.s.task_completion(self.id)
        reopened = self.complete(False)
        self.assertFalse(reopened['completed'])
        self.assertIsNone(reopened['completion_confirmed_at'])
        self.assertEqual(reopened['label'], 'pending')
        self.assertNotEqual(before['completion_generation'], reopened['completion_generation'])
        for job in (derive_job, evaluation_job):
            self.assertEqual(self.s.one('SELECT status FROM jobs WHERE id=?', (job,))['status'], 'cancelled')
        self.assertEqual(self.s.one("SELECT status FROM candidates WHERE id='c'")['status'], 'stale')
        self.assertEqual(self.s.one("SELECT status FROM candidates WHERE id='active'")['status'], 'accepted')
        count = len(self.s.rows('SELECT * FROM audit'))
        self.assertTrue(self.complete(False)['reused'])
        self.assertEqual(len(self.s.rows('SELECT * FROM audit')), count)
        self.assertEqual([r['label'] for r in self.s.rows('SELECT * FROM labels ORDER BY created')], ['correct', 'pending'])
        with self.assertRaises(Invalid):
            self.app.action({'action': 'job.retry', 'id': derive_job})
        reconfirmed = self.complete()
        self.assertNotEqual(before['completion_generation'], reconfirmed['completion_generation'])
        self.assertEqual(reconfirmed['label'], 'pending')

    def test_label_roundtrip_revokes_generation_and_duplicate_is_noop(self):
        first = self.complete()
        self.assertEqual(first['completion_generation'], self.complete()['completion_generation'])
        self.label()
        binding = self.s.task_completion(self.id)
        self.assertTrue(self.label()['reused'])
        self.assertEqual(binding, self.s.task_completion(self.id))
        self.label('incorrect')
        self.label('correct')
        self.assertNotEqual(binding['completion_generation'], self.s.task_completion(self.id)['completion_generation'])

    def test_boolean_validation_and_unknown_id(self):
        for value in (1, 'true', None):
            with self.assertRaises(Invalid): self.complete(value)
        with self.assertRaises(Invalid):
            self.app.action({'action': 'episode.complete', 'id': 'missing', 'completed': True})

    def test_historical_migration_once_and_auto_promote_disabled(self):
        self.s.execute("UPDATE episodes SET label='correct' WHERE id=?", (self.id,))
        self.s.execute("DELETE FROM kv WHERE key='human_completion_migration_v1'")
        cfg = self.s.get('config'); cfg['auto_promote'] = True; self.s.set('config', cfg)
        self.s.db.close()
        self.s = Store(Path(self.tmp.name) / 'state'); self.app.store = self.s
        self.assertTrue(self.s.task_completed(self.id))
        self.assertFalse(self.s.get('config')['auto_promote'])
        with self.assertRaises(Invalid): configure(self.s.get('config'), {'auto_promote': True})
        self.complete(False)
        self.s.db.close()
        self.s = Store(Path(self.tmp.name) / 'state'); self.app.store = self.s
        self.assertFalse(self.s.task_completed(self.id))

    def test_worker_never_promotes_even_if_config_is_tampered(self):
        cfg = self.s.get('config'); cfg['auto_promote'] = True; self.s.set('config', cfg)
        self.s.job('evaluate', 'candidate')
        def evaluated(*args):
            self.app.stop.set()
            return {'experiment_id': 'experiment'}
        original_claim=self.s.claim
        def claim():
            row=original_claim()
            if not row:self.app.stop.set()
            return row
        with patch.object(self.app,'readiness',return_value={'ready':True}), \
                patch.object(self.s,'claim',side_effect=claim), \
                patch('foundry_loop.evaluate.evaluate_candidate', side_effect=evaluated) as evaluate, \
                patch('foundry_loop.evaluate.promote') as promote:
            self.app.worker()
            evaluate.assert_called_once()
            promote.assert_not_called()

    def test_worker_rejects_direct_derive_job_for_unconfirmed_episode(self):
        job = self.s.job('derive', self.id)
        original = self.s.claim
        def claim():
            row = original()
            if not row: self.app.stop.set()
            return row
        with patch.object(self.s, 'claim', side_effect=claim), patch('foundry_loop.app.derive') as derive:
            self.app.worker()
            derive.assert_not_called()
        self.assertEqual(self.s.one('SELECT status FROM jobs WHERE id=?', (job,))['status'], 'failed')


if __name__ == '__main__': unittest.main()
