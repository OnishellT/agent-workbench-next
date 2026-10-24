import hashlib
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from foundry_loop.app import App, configure
from foundry_loop.common import Invalid
from foundry_loop.ingest import _read, register_source, scan_sources
from foundry_loop.store import Store
import foundry_loop.ingest as ingest


class StreamingCaptureTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.root=Path(self.temp.name)
        self.store=Store(self.root/'state');self.path=self.root/'log.jsonl'
        self.path.write_text('{"role":"user","text":"First"}\n{"role":"assistant","text":"Second"}\n')

    def tearDown(self):
        self.store.db.close();self.temp.cleanup()

    def test_jsonl_never_reads_whole_file_and_keeps_hash(self):
        original=ingest.os.fdopen;calls=[]
        class Reader:
            def __init__(self,stream):self.stream=stream
            def __enter__(self):return self
            def __exit__(self,*args):return self.stream.__exit__(*args)
            def fileno(self):return self.stream.fileno()
            def readline(self,size):calls.append(size);return self.stream.readline(size)
            def read(self,*args):raise AssertionError('JSONL must not materialize a whole-file read')
        with patch.object(ingest.os,'fdopen',side_effect=lambda *a,**kw:Reader(original(*a,**kw))):
            events,coverage=_read(self.path,'canonical',1024)
        self.assertEqual(len(events),2);self.assertGreaterEqual(len(calls),2)
        self.assertEqual(coverage['raw_sha256'],hashlib.sha256(self.path.read_bytes()).hexdigest())
        self.assertEqual(coverage['bytes'],self.path.stat().st_size)
        self.assertEqual(coverage['status'],'complete')
        with self.assertRaisesRegex(Invalid,'source_byte_limit'):_read(self.path,'canonical',10)

    def test_configured_maximum_reaches_reader_without_hidden_64mb_cap(self):
        register_source(self.store,self.path,'canonical')
        cfg=configure(self.store.get('config'),{'max_source_bytes':512000000});self.store.set('config',cfg)
        with patch.object(ingest,'_capture',return_value=0) as capture:
            scan_sources(self.store)
        self.assertEqual(capture.call_args.args[-1],512000000)
        with self.assertRaises(Invalid):configure(cfg,{'max_source_bytes':512000001})

    def test_normalizer_upgrade_recaptures_unchanged_files_once(self):
        register_source(self.store,self.path,'canonical')
        self.assertEqual(scan_sources(self.store)['changed'],1)
        self.assertEqual(scan_sources(self.store)['changed'],0)
        with patch.object(ingest,'NORMALIZER_VERSION','test-next-schema'):
            self.assertEqual(scan_sources(self.store)['changed'],1)
            self.assertEqual(scan_sources(self.store)['changed'],0)
        self.assertEqual(len(self.store.rows('SELECT * FROM revisions')),2)

    def test_retained_semantic_issues_are_distinct_from_lost_records(self):
        retained={'line':1,'reason':'unknown_semantics','retained':True,'severity':'semantic'}
        original=ingest._normalize
        def advisory(records,adapter,fallback):
            events,issues=original(records,adapter,fallback)
            return events,issues+[retained]
        with patch.object(ingest,'_normalize',side_effect=advisory):
            _,coverage=_read(self.path,'canonical',1024)
        self.assertEqual(coverage['status'],'complete');self.assertEqual(coverage['semantic_issue_count'],1)
        self.assertEqual(coverage['blocking_issue_count'],0)

    def test_guided_example_capture_works_while_background_capture_paused(self):
        self.store.db.close()
        app=App(self.root/'state');self.store=app.store
        cfg=self.store.get('config');cfg.update(capture=False,max_files_per_scan=1);self.store.set('config',cfg)
        result=app.example()
        episodes=self.store.rows('SELECT * FROM episodes WHERE session_id=?',(result['session_id'],))
        self.assertEqual(len(episodes),1);self.assertEqual(episodes[0]['label'],'pending')
        self.assertEqual(len(result['case_ids']),2)


if __name__=='__main__':unittest.main()
