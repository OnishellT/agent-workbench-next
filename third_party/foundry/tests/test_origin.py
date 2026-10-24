import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from foundry_loop.app import App
from foundry_loop.client import run_task
from foundry_loop.common import Invalid, digest, js, now, write_json
from foundry_loop.integrations import active_context
from foundry_loop.origin import confirm_origin
from foundry_loop.snapshots import create_case


class OriginTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.app = App(self.root / 'state'); self.store = self.app.store
        self.project = self.root / 'project'; self.project.mkdir()
        (self.project / 'state.txt').write_text('bad')
        self.oracle = self.root / 'oracle'; self.oracle.mkdir()
        (self.oracle / 'verify.py').write_text("import pathlib,sys\nassert (pathlib.Path(sys.argv[1])/'state.txt').read_text() == 'good'\n")
        (self.oracle / 'good.py').write_text("import pathlib,sys\n(pathlib.Path(sys.argv[1])/'state.txt').write_text('good')\n")
        (self.oracle / 'bad.py').write_text("import pathlib,sys\n(pathlib.Path(sys.argv[1])/'state.txt').write_text('bad')\n")
        self.prompt = 'Fix state.txt and verify public behavior.'
        self.case = create_case(self.store, {'project_path': str(self.project), 'verifier_path': str(self.oracle),
            'title': 'Initial state', 'prompt': self.prompt, 'domain': 'fixture', 'split': 'origin',
            'verify': ['{python}', '{verifier}/verify.py', '{workspace}'],
            'positive_control': ['{python}', '{verifier}/good.py', '{workspace}'],
            'negative_control': ['{python}', '{verifier}/bad.py', '{workspace}']})
        cfg = self.store.get('config'); cfg['auto_derive'] = False; self.store.set('config', cfg)

    def tearDown(self):
        self.store.db.close(); self.tmp.cleanup()

    def driver(self, config, prompt, workspace, **kwargs):
        self.actual_provider = config
        (Path(workspace) / 'state.txt').write_text('good')
        return {'text': 'Mechanical change executed.', 'real_inference': False, 'usage': {}, 'seconds': 0}

    def task(self, prompt=None, cases=None):
        with patch('foundry_loop.providers.run_agent', side_effect=self.driver):
            return run_task(self.store, self.project, 'fixture', prompt or self.prompt,
                            [self.case['id']] if cases is None else cases)

    def test_matching_initial_tree_and_prompt_are_bound_before_edit(self):
        result = self.task()
        binding = self.store.get('episode_origin:' + result['episode_id'])
        manifest = json.loads(self.case['snapshot'])
        self.assertEqual(binding['project_hash'], manifest['project_hash'])
        self.assertEqual(binding['prompt_hash'], digest(self.prompt))
        self.assertEqual(binding['provider_config_hash'], digest(self.actual_provider))
        self.assertEqual(binding['provenance'], 'system_recorded')
        self.assertTrue(binding['captured_before_task'])
        self.assertEqual((self.project / 'state.txt').read_text(), 'good')
        episode = self.store.one('SELECT * FROM episodes WHERE id=?', (result['episode_id'],))
        self.assertEqual(binding['source_revision'], episode['revision'])
        self.assertEqual(episode['label'], 'pending')
        self.assertFalse(self.store.task_completed(episode['id']))

    def test_missing_or_mismatched_origin_does_not_claim_replay_provenance(self):
        for mismatch in ('missing', 'prompt', 'tree', 'project'):
            with self.subTest(mismatch=mismatch):
                (self.project / 'state.txt').write_text('different' if mismatch == 'tree' else 'bad')
                prompt = self.prompt + ' A different task.' if mismatch == 'prompt' else self.prompt
                if mismatch == 'project':
                    other = self.root / 'other'; other.mkdir(); (other / 'state.txt').write_text('bad')
                    old = self.project; self.project = other
                try:
                    result = self.task(prompt, [] if mismatch == 'missing' else None)
                    self.assertIsNone(self.store.get('episode_origin:' + result['episode_id']))
                finally:
                    if mismatch == 'project': self.project = old

    def test_human_attestation_requires_explicit_true_linked_origin_and_note(self):
        result = self.task(self.prompt + ' historical import')
        episode_id = result['episode_id']
        for confirmed in (False, None, 1, 'true'):
            with self.assertRaises(Invalid): confirm_origin(self.store, episode_id, self.case['id'], confirmed, 'Commit reviewed')
        with self.assertRaises(Invalid): confirm_origin(self.store, episode_id, self.case['id'], True, '')
        self.store.execute("UPDATE episodes SET cases_json='[]' WHERE id=?", (episode_id,))
        with self.assertRaises(Invalid): confirm_origin(self.store, episode_id, self.case['id'], True, 'Commit reviewed')
        self.store.execute('UPDATE episodes SET cases_json=? WHERE id=?', (js([self.case['id']]), episode_id))
        binding = self.app.action({'action': 'episode.origin', 'id': episode_id, 'case_id': self.case['id'],
                                   'confirmed_initial_state': True, 'note': 'Commit reviewed by human'})
        self.assertEqual(binding['provenance'], 'human_attested')
        self.assertFalse(self.store.task_completed(episode_id))
        self.assertEqual(self.store.enrich_episode(self.store.one('SELECT * FROM episodes WHERE id=?', (episode_id,)))['origin'], binding)

    def test_tampered_snapshot_cannot_be_attested(self):
        result = self.task(self.prompt + ' imported')
        manifest = json.loads(self.case['snapshot'])
        (self.store.root / manifest['directory'] / 'project' / 'state.txt').write_text('tampered')
        with self.assertRaises(Invalid):
            confirm_origin(self.store, result['episode_id'], self.case['id'], True, 'I checked the commit')

    def test_provider_config_binding_uses_configuration_actually_dispatched(self):
        from foundry_loop.ingest import register_source
        def change_config(*args):
            cfg = self.store.get('config'); cfg['replay']['model'] = 'changed-during-capture'
            self.store.set('config', cfg)
            return register_source(*args)
        with patch('foundry_loop.ingest.register_source', side_effect=change_config):
            result = self.task()
        binding = self.store.get('episode_origin:' + result['episode_id'])
        self.assertEqual(binding['provider_config_hash'], digest(self.actual_provider))

    def test_initial_tree_changed_before_dispatch_does_not_claim_matching_origin(self):
        from foundry_loop.ingest import register_source
        def change_tree(*args):
            (self.project / 'state.txt').write_text('concurrent change before dispatch')
            return register_source(*args)
        with patch('foundry_loop.ingest.register_source', side_effect=change_tree):
            result = self.task()
        self.assertIsNone(self.store.get('episode_origin:' + result['episode_id']))

    def test_legacy_capability_without_completion_generation_cannot_reactivate(self):
        result = self.task(); episode_id = result['episode_id']
        self.app.action({'action': 'episode.complete', 'id': episode_id, 'completed': True})
        self.app.action({'action': 'episode.label', 'id': episode_id, 'label': 'correct'})
        episode = self.store.one('SELECT * FROM episodes WHERE id=?', (episode_id,))
        content = {'instructions': 'Inspect public behavior.', 'files': []}
        spec = {'name': 'legacy', 'description': 'Legacy rule', 'kind': 'rule', 'scope': 'domain', 'domain': 'fixture',
                'positive': content, 'episode_binding': digest({key: episode[key] for key in ('revision', 'label', 'feedback', 'first_event', 'last_event')})}
        self.store.execute('INSERT INTO candidates VALUES (?,?,?,?,?,?)', ('candidate_a', episode_id, js(spec), 'accepted', 'initial', now()))
        members = [{'candidate_id': 'candidate_a', 'variant': 'positive'}]
        package = {'schema': 1, 'id': 'version_a', 'parent': 'initial', 'members': members, 'experiment_id': 'fixture',
                   'artifacts': [{'candidate_id': 'candidate_a', 'variant': 'positive', 'name': 'legacy', 'kind': 'rule',
                                  'scope': 'domain', 'domain': 'fixture', 'content': content}]}
        write_json(self.store.root / 'registry' / 'version_a' / 'manifest.json', package)
        self.store.execute('INSERT INTO versions VALUES (?,?,?,?,?)', ('version_a', 'initial', js(members), now(), 'Fixture'))
        self.store.audit('registry.promoted', {'version': 'version_a', 'package_hash': digest(package)})
        self.store.set('active_version', 'version_a')
        self.app.action({'action': 'episode.label', 'id': episode_id, 'label': 'incorrect'})
        self.app.action({'action': 'episode.label', 'id': episode_id, 'label': 'correct'})
        context = active_context(self.store, 'fixture', str(self.project))
        self.assertEqual(context['capabilities'], [])
        self.assertTrue(context['warnings'])


if __name__ == '__main__': unittest.main()
