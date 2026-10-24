import json
from pathlib import Path
import tempfile
import unittest

from foundry_loop.common import Invalid
from foundry_loop.ingest import register_source, scan_sources, session_detail, create_episode
from foundry_loop.store import Store


class IngestionTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.store = Store(self.root / 'store')
        self.source = self.root / 'sources'
        self.source.mkdir()

    def tearDown(self):
        self.store.db.close()
        self.tmp.cleanup()

    def write(self, records, name='log.jsonl'):
        path = self.source / name
        path.write_text(''.join(json.dumps(x) + '\n' for x in records))
        return path

    def detail(self):
        row = self.store.one('SELECT id FROM sessions ORDER BY updated DESC LIMIT 1')
        return session_detail(self.store, row['id'])

    def test_current_codex_missing_ids_redaction_tools_and_completion(self):
        path = self.write([
            {'type': 'session_meta', 'payload': {'id': 'session-native', 'parent_session_id': 'parent'}},
            {'type': 'response_item', 'payload': {'type': 'message', 'role': 'user', 'content': [{'type': 'input_text', 'text': 'Check api_key=sk-testing123456789'}, {'type': 'input_image', 'image_url': 'data:image/png;base64,aGVsbG8='}]}},
            {'type': 'response_item', 'payload': {'type': 'function_call', 'call_id': 'call1', 'name': 'shell', 'arguments': '{"cmd":"pwd"}'}},
            {'type': 'response_item', 'payload': {'type': 'function_call_output', 'call_id': 'call1', 'output': '/work'}},
            {'type': 'response_item', 'payload': {'type': 'message', 'role': 'assistant', 'channel': 'final', 'content': [{'type': 'output_text', 'text': 'Done'}]}},
            {'type': 'event_msg', 'payload': {'type': 'task_complete'}},
        ])
        register_source(self.store, path, 'codex')
        self.assertEqual(scan_sources(self.store)['changed'], 1)
        detail = self.detail()
        self.assertEqual(detail['completion'], 'suggested')
        self.assertEqual(detail['coverage']['status'], 'complete')
        self.assertEqual(len(detail['episodes']), 1)
        self.assertEqual(detail['episodes'][0]['label'], 'pending')
        self.assertEqual(detail['events'][1]['id_origin'], 'generated')
        self.assertEqual(detail['events'][1]['parent_session_id'], 'parent')
        self.assertEqual(detail['events'][2]['call_id'], detail['events'][3]['call_id'])
        persisted = '\n'.join(r['events'] for r in self.store.rows('SELECT events FROM revisions'))
        self.assertNotIn('sk-testing123456789', persisted)
        self.assertNotIn('aGVsbG8=', persisted)
        self.assertEqual(scan_sources(self.store)['changed'], 0)

    def test_revisions_and_manual_episode_are_immutable_on_edit_rotation(self):
        path = self.write([{'role': 'user', 'text': 'Original'}, {'role': 'assistant', 'text': 'Yes'}])
        register_source(self.store, path, 'canonical')
        scan_sources(self.store)
        old = self.detail()
        ep = create_episode(self.store, old['id'], title='Review')
        replacement = self.write([{'role': 'user', 'text': 'Changed'}, {'role': 'assistant', 'text': 'No'}], 'replacement.jsonl')
        replacement.replace(path)
        self.assertEqual(scan_sources(self.store)['changed'], 1)
        new = self.detail()
        self.assertEqual(old['id'], new['id'])
        self.assertNotEqual(old['revision'], new['revision'])
        self.assertEqual(self.store.one('SELECT revision FROM episodes WHERE id=?', (ep['id'],))['revision'], old['revision'])
        self.assertEqual(len(new['revisions']), 2)
        with self.assertRaises(Invalid):
            create_episode(self.store, old['id'], -1)

    def test_partial_unsupported_and_malformed_are_visible_then_recover(self):
        path = self.write([{'role': 'user', 'text': 'Hi'}, {'type': 'unknown_native_kind'}])
        with path.open('a') as f:
            f.write('{"role":')
        register_source(self.store, path, 'pi')
        scan_sources(self.store)
        coverage = self.detail()['coverage']
        self.assertEqual(coverage['status'], 'partial')
        self.assertEqual(coverage['issue_count'], 2)
        self.assertEqual(coverage['issues'][-1]['reason'], 'unsupported_record')
        path.write_text('{"role":"user","text":"Repaired"}\n')
        scan_sources(self.store)
        self.assertEqual(self.detail()['coverage']['status'], 'complete')

    def test_directory_fairness_and_no_symlink_credentials(self):
        for index in range(9):
            self.write([{'role': 'user', 'text': f'Task {index}'}], f'{index}.jsonl')
        self.write([{'role': 'user', 'text': 'Credential file'}], 'auth.json')
        external = self.root / 'external.jsonl'
        external.write_text('{"role":"user","text":"Outside"}\n')
        (self.source / 'linked.jsonl').symlink_to(external)
        config = self.store.get('config'); config['max_files_per_scan'] = 2; self.store.set('config', config)
        register_source(self.store, self.source, 'canonical')
        for _ in range(6):
            self.assertLessEqual(scan_sources(self.store)['files'], 2)
        self.assertEqual(len(self.store.rows('SELECT * FROM sessions')), 9)
        with self.assertRaises(Invalid):
            register_source(self.store, self.source / 'auth.json', 'canonical')
        with self.assertRaises(Invalid):
            register_source(self.store, self.source / 'linked.jsonl', 'canonical')

    def test_native_claude_pi_omp_and_canonical_container(self):
        fixtures = {
            'claude': [{'type': 'user', 'uuid': 'u1', 'sessionId': 's1', 'message': {'role': 'user', 'content': 'Build'}}, {'type': 'assistant', 'uuid': 'a1', 'parentUuid': 'u1', 'message': {'role': 'assistant', 'content': [{'type': 'tool_use', 'id': 't1', 'name': 'Read', 'input': {'path': '/work'}}]}}],
            'pi': [{'type': 'session', 'id': 'p1'}, {'type': 'message', 'id': 'u1', 'parentId': None, 'message': {'role': 'user', 'content': [{'type': 'text', 'text': 'Build'}]}}],
            'omp': [{'type': 'message', 'id': 'm1', 'message': {'role': 'toolResult', 'toolCallId': 't1', 'toolName': 'read', 'content': [{'type': 'text', 'text': 'Result'}]}}],
        }
        for adapter, records in fixtures.items():
            path = self.write(records, adapter + '.jsonl')
            register_source(self.store, path, adapter)
        container = self.source / 'container.json'
        container.write_text(json.dumps({'session_id': 'canonical-session', 'events': [{'role': 'user', 'text': 'Work'}]}))
        register_source(self.store, container, 'canonical')
        self.assertEqual(scan_sources(self.store)['changed'], 4)
        for session in self.store.rows('SELECT id FROM sessions'):
            self.assertEqual(session_detail(self.store, session['id'])['coverage']['status'], 'complete')

    def test_size_limit_reported_without_false_success(self):
        path = self.write([{'role': 'user', 'text': 'x' * 1000}])
        register_source(self.store, path, 'canonical')
        config = self.store.get('config'); config['max_source_bytes'] = 100; self.store.set('config', config)
        result = scan_sources(self.store)
        self.assertEqual(result['errors'], 1)
        self.assertEqual(result['changed'], 0)
        self.assertEqual(self.store.one('SELECT error FROM sources')['error'], 'source_byte_limit')
        self.assertEqual(self.store.rows('SELECT * FROM file_marks'), [])

    def test_appending_new_task_does_not_duplicate_existing_auto_episode(self):
        path = self.write([{'role': 'user', 'text': 'First'}, {'type': 'task_complete'}])
        register_source(self.store, path, 'canonical')
        scan_sources(self.store)
        original = self.detail()['episodes'][0]
        with path.open('a') as f:
            f.write(json.dumps({'role': 'user', 'text': 'Second'}) + '\n' + json.dumps({'type': 'task_complete'}) + '\n')
        scan_sources(self.store)
        episodes = self.detail()['episodes']
        self.assertEqual(len(episodes), 2)
        self.assertEqual(episodes[0]['id'], original['id'])
        self.assertEqual(episodes[1]['first_event'], 2)


if __name__ == '__main__':
    unittest.main()
