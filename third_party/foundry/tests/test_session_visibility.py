"""Activity must remain discoverable independently of historical import order."""
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from foundry_loop.app import App


class SessionVisibilityTests(unittest.TestCase):
    def test_recent_activity_survives_list_cap_and_backfill_uses_file_time(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            app=App(root/'state')
            try:
                for i in range(501):
                    app.store.execute('INSERT INTO sessions VALUES (?,?,?,?,?,?,?,?)',
                        (f'session_{i}','source','/missing',f'Import {i}','r',5000+i,
                         'unknown',json.dumps({'activity_at':100+i})))
                path=root/'active.jsonl';path.write_text('{}\n')
                os.utime(path,(10000,10000))
                app.store.execute('INSERT INTO sessions VALUES (?,?,?,?,?,?,?,?)',
                    ('live','source',str(path),'Active conversation','r',1,'unknown','{}'))
                with patch('foundry_loop.app.diagnostics',return_value={}):
                    state=app.state()
                self.assertEqual(state['total_sessions'],502)
                self.assertEqual(len(state['sessions']),500)
                self.assertEqual(state['sessions'][0]['id'],'live')
                self.assertEqual(state['sessions'][0]['activity_at'],10000)
                self.assertEqual(state['sessions'][0]['captured_at'],1)
                self.assertEqual(state['sessions'][1]['id'],'session_500')
            finally:
                app.store.db.close()


if __name__=='__main__':
    unittest.main()
