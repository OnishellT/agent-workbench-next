import itertools
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from foundry_loop.common import Invalid, js
import foundry_loop.ingest as ingest
from foundry_loop.store import Store


class LiveCaptureTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.root=Path(self.temp.name);self.store=Store(self.root/'state')
        self.path=self.root/'active.jsonl';self.path.write_text('{"role":"user","text":"First request"}\n')
    def tearDown(self):
        self.store.db.close();self.temp.cleanup()

    def test_append_during_read_captures_verified_initial_prefix_then_new_events(self):
        original=ingest._normalize
        def append(records,adapter,fallback):
            iterator=iter(records);first=next(iterator)
            with self.path.open('a') as output:output.write('{"role":"assistant","text":"Later response"}\n')
            return original(itertools.chain([first],iterator),adapter,fallback)
        with patch.object(ingest,'_normalize',side_effect=append):
            events,coverage=ingest._read(self.path,'canonical',10000)
        self.assertEqual(len(events),1);self.assertTrue(coverage['append_pending'])
        self.assertEqual(coverage['status'],'complete')
        self.assertLess(coverage['snapshot_bytes'],self.path.stat().st_size)
        events,coverage=ingest._read(self.path,'canonical',10000)
        self.assertEqual(len(events),2);self.assertFalse(coverage['append_pending'])

    def test_rewrite_during_read_is_rejected_even_with_same_file_size(self):
        original=ingest._normalize
        def rewrite(records,adapter,fallback):
            iterator=iter(records);first=next(iterator)
            self.path.write_text(self.path.read_text().replace('First','Other'))
            return original(itertools.chain([first],iterator),adapter,fallback)
        with patch.object(ingest,'_normalize',side_effect=rewrite),self.assertRaisesRegex(Invalid,'source_changed_retryable'):
            ingest._read(self.path,'canonical',10000)

    def test_human_title_and_activity_ignore_envelopes_and_ingestion_time(self):
        events=[{'role':'user','text':'# AGENTS.md instructions for /project\n<INSTRUCTIONS>Policy</INSTRUCTIONS>'},
                {'role':'user','text':'<environment_context>machine context</environment_context>'},
                {'role':'user','text':'<external_codex_apps_open_page>{"page_id":null}</external_codex_apps_open_page>'},
                {'role':'user','text':'# Files mentioned by the user:\nfile.png\n## My request:\nQuiero revisar las conversaciones de hoy.','timestamp':'2026-10-13T00:15:00-04:00'},
                {'role':'assistant','text':'Result','raw_record':{'timestamp':'2026-10-13T04:17:00Z'}}]
        self.assertEqual(ingest.session_title(events),'Quiero revisar las conversaciones de hoy.')
        self.assertEqual(ingest.session_activity(events),1791865020)
        self.assertEqual(ingest.session_activity([],1700000000),1700000000)

    def test_metadata_refresh_keeps_revision_and_human_label_immutable(self):
        ingest.register_source(self.store,self.path,'canonical');ingest.scan_sources(self.store)
        session=self.store.one('SELECT * FROM sessions');episode=ingest.create_episode(self.store,session['id'])
        self.store.execute("UPDATE episodes SET label='correct',feedback='Human accepted' WHERE id=?",(episode['id'],))
        coverage=json.loads(session['coverage']);coverage.pop('display_metadata_version');coverage.pop('activity_at')
        self.store.execute('UPDATE sessions SET title=?,coverage=? WHERE id=?',('environment_context',js(coverage),session['id']))
        self.assertEqual(ingest.refresh_session_metadata(self.store)['updated'],1)
        repaired=self.store.one('SELECT * FROM sessions WHERE id=?',(session['id'],))
        self.assertEqual(repaired['title'],'First request');self.assertEqual(repaired['revision'],session['revision'])
        self.assertEqual(json.loads(repaired['coverage'])['activity_basis'],'source_mtime')
        unchanged=self.store.one('SELECT label,feedback,revision FROM episodes WHERE id=?',(episode['id'],))
        self.assertEqual(unchanged['label'],'correct');self.assertEqual(unchanged['feedback'],'Human accepted')
        self.assertEqual(unchanged['revision'],episode['revision'])

    def test_recent_old_started_file_and_new_day_are_discovered_without_starvation(self):
        tree=self.root/'sessions';tree.mkdir()
        for index in range(8):
            path=tree/f'{index:02}.jsonl';path.write_text(js({'role':'user','text':f'Request {index}'})+'\n');os.utime(path,(1000+index,1000+index))
        ingest.register_source(self.store,tree,'canonical')
        cfg=self.store.get('config');cfg.update(max_files_per_scan=2,max_scan_seconds=3);self.store.set('config',cfg)
        ingest.scan_sources(self.store)
        latest=tree/'01.jsonl';latest.write_text(js({'role':'user','text':'Old chat active today'})+'\n')
        newdir=tree/'2026'/'10'/'13';newdir.mkdir(parents=True)
        newest=newdir/'new.jsonl';newest.write_text(js({'role':'user','text':'Brand new chat'})+'\n')
        for _ in range(9):ingest.scan_sources(self.store)
        self.assertEqual(len(self.store.rows('SELECT * FROM sessions')),9)
        self.assertIsNotNone(self.store.one('SELECT id FROM sessions WHERE path=?',(str(newest),)))
        self.assertEqual(self.store.one('SELECT title FROM sessions WHERE path=?',(str(latest),))['title'],'Old chat active today')

    def test_recent_rotation_survives_each_first_file_exhausting_deadline(self):
        tree=self.root/'recent';tree.mkdir()
        for index in range(10):
            path=tree/f'{index:02}.jsonl';path.write_text(js({'role':'user','text':'Request'})+'\n')
            os.utime(path,(1000+index,1000+index))
        ingest.register_source(self.store,tree,'canonical')
        cfg=self.store.get('config');cfg.update(max_files_per_scan=200,max_scan_seconds=1);self.store.set('config',cfg)
        clock=[10000.0];captured=[]
        def slow_capture(store,source,path,max_bytes):
            captured.append(path.name)
            clock[0]+=2  # One file exceeds the configured scan deadline.
            return 1
        with patch.object(ingest.time,'monotonic',side_effect=lambda:clock[0]),patch.object(ingest,'_capture',side_effect=slow_capture):
            for _ in range(20):
                result=ingest.scan_sources(self.store)
                self.assertEqual(result['files'],1)
        # Priority and historical scans alternate; every priority scan advances
        # despite capturing only its first file. None of the ten recents starves.
        self.assertEqual(captured[::2],[f'{index:02}.jsonl' for index in reversed(range(10))])

    def test_recent_fairness_uses_paths_when_mtimes_reorder_every_scan(self):
        tree=self.root/'moving';tree.mkdir()
        paths=[]
        for index in range(10):
            path=tree/f'{index:02}.jsonl';path.write_text(js({'role':'user','text':'Request'})+'\n');paths.append(path)
        source=ingest.register_source(self.store,tree,'canonical')
        seen=[]
        for iteration in range(10):
            # Previously visited paths can become the newest again while other
            # paths move between array indices. Last-attempt identity still wins.
            for index,path in enumerate(paths):
                stamp=10000+((index+iteration*3)%10)
                os.utime(path,(stamp,stamp))
            self.store.set('capture_priority_cycle:'+source['id'],iteration*2)
            iterator=ingest._scan_paths(self.store,source,ingest.time.monotonic()+10,200)
            seen.append(next(iterator).name)
            iterator.close()
        self.assertEqual(len(set(seen)),10)
        self.assertEqual(set(seen),{path.name for path in paths})

    def test_new_scheduler_prioritizes_oldest_known_capture(self):
        tree=self.root/'stale';tree.mkdir()
        source=ingest.register_source(self.store,tree,'canonical')
        for index in range(10):
            path=tree/f'{index:02}.jsonl';path.write_text(js({'role':'user','text':'Request'})+'\n')
            os.utime(path,(1000+index,1000+index))
            self.store.execute('INSERT INTO file_marks VALUES (?,?,?)',(source['id']+':'+str(path),'oldfingerprint',10 if index==3 else 1000))
        iterator=ingest._scan_paths(self.store,source,ingest.time.monotonic()+10,200)
        self.assertEqual(next(iterator).name,'03.jsonl')
        iterator.close()


if __name__=='__main__':unittest.main()
