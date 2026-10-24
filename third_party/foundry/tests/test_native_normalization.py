import json
import unittest

from foundry_loop.ingest import _normalize


class NativeNormalizationTests(unittest.TestCase):
    def normalize(self, records, adapter='codex'):
        return _normalize(list(enumerate(records, 1)), adapter, 'fixture-session')

    def test_unknown_codex_records_retained_without_claiming_semantics(self):
        records = [
            {'type': 'world_state', 'payload': {'state': {'instructions': 'Historical inert instruction'}}},
            {'type': 'token_usage_record', 'payload': {'usage': {'input_tokens': 123}}},
            {'type': 'future_format', 'payload': ['new', {'value': 42}]},
        ]
        events, issues = self.normalize(records)
        self.assertEqual(len(events), len(records))
        self.assertTrue(all(issue['retained'] for issue in issues))
        self.assertEqual([event['raw_record'] for event in events], records)
        self.assertTrue(all(event['inert'] and event['semantic_support'] == 'raw_only' for event in events))
        self.assertFalse(any(event.get('completion_suggested') for event in events))

    def test_new_codex_completed_message_body_and_phase(self):
        records = [{'type': 'event_msg', 'ordinal': 7, 'payload': {'type': 'item_completed', 'item': {
            'type': 'AgentMessage', 'id': 'a1', 'phase': 'final_answer',
            'content': [{'type': 'text', 'text': 'Verified final artifact.'}]}}}]
        events, issues = self.normalize(records)
        self.assertFalse(issues)
        self.assertEqual(events[0]['text'], 'Verified final artifact.')
        self.assertTrue(events[0]['completion_suggested'])
        self.assertEqual(events[0]['native_sequence'], 7)
        self.assertEqual(events[0]['raw_record'], records[0])

    def test_response_agent_message_uses_content(self):
        events, issues = self.normalize([{'type': 'response_item', 'payload': {
            'type': 'agent_message', 'author': 'assistant', 'channel': 'final',
            'content': [{'type': 'output_text', 'text': 'Finished.'}]}}])
        self.assertFalse(issues)
        self.assertEqual(events[0]['text'], 'Finished.')
        self.assertTrue(events[0]['completion_suggested'])

    def test_non_codex_metadata_and_credential_pin_are_safe(self):
        records = [
            {'type': 'custom_message', 'content': 'An inert historical instruction', 'customType': 'note'},
            {'type': 'session_init', 'systemPrompt': ['Historical policy'], 'tools': ['read']},
            {'type': 'credential_pin', 'provider': 'example', 'key': 'private-fixture-value', 'api_key': 'sk-test-secretfixture123'},
        ]
        events, issues = self.normalize(records, 'omp')
        self.assertEqual(len(events), 3)
        self.assertTrue(all(issue['retained'] for issue in issues))
        self.assertEqual(events[0]['raw_record'], records[0])
        serialized = json.dumps(events)
        self.assertNotIn('private-fixture-value', serialized)
        self.assertNotIn('sk-test-secretfixture123', serialized)
        self.assertIn('[REDACTED:metadata:', serialized)

    def test_unknown_content_retained_and_known_record_metadata_not_dropped(self):
        record = {'role': 'user', 'text': 'outer', 'content': [{'type': 'future_part', 'body': {'value': 42}}], 'extension': {'branch': 'b'}}
        events, issues = self.normalize([record], 'canonical')
        self.assertTrue(issues[0]['retained'])
        self.assertEqual(events[0]['raw_record'], record)
        self.assertEqual(events[0]['attachments'][0]['raw_content']['body']['value'], 42)

    def test_real_redaction_limit_remains_blocking(self):
        events, issues = self.normalize([{'type': 'new_format', 'payload': {'blob': 'x' * 1_048_577}}])
        self.assertEqual(events, [])
        self.assertTrue(any(issue['reason'] == 'redaction_limit' and not issue.get('retained') for issue in issues))


if __name__ == '__main__':
    unittest.main()
