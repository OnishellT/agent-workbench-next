import json, tempfile, threading, time, unittest
from pathlib import Path
from unittest.mock import patch
from foundry_loop.app import App
from foundry_loop.ingest import register_source
from foundry_loop.common import now

class LifecycleTests(unittest.TestCase):
    def test_ingestion_continues_while_model_job_is_busy(self):
        with tempfile.TemporaryDirectory()as d:
            root=Path(d);sources=root/'sources';sources.mkdir()
            app=App(root/'state');cfg=app.store.get('config');cfg['poll_seconds']=.05;app.store.set('config',cfg)
            register_source(app.store,str(sources),'canonical')
            entered=threading.Event();release=threading.Event()
            def busy(*args):entered.set();release.wait(5);return {'ok':True}
            app.store.execute('INSERT INTO episodes VALUES (?,?,?,?,?,?,?,?,?,?)',
                              ('explicit-test-task','test-session','test-revision','Busy test',0,0,'pending','','[]',now()))
            app.action({'action':'episode.complete','id':'explicit-test-task','completed':True})
            cfg=app.store.get('config');cfg['auto_derive']=False;app.store.set('config',cfg)
            app.action({'action':'episode.label','id':'explicit-test-task','label':'correct'})
            with patch('foundry_loop.app.derive',side_effect=busy):
                app.store.job('derive','explicit-test-task');app.start_worker()
                try:
                    self.assertTrue(entered.wait(2))
                    (sources/'new-task.jsonl').write_text(json.dumps({'role':'user','text':'Incoming while model is busy'})+'\n')
                    end=time.monotonic()+3
                    while time.monotonic()<end and not app.store.rows('SELECT * FROM sessions'):time.sleep(.03)
                    self.assertEqual(len(app.store.rows('SELECT * FROM sessions')),1)
                    self.assertFalse(release.is_set())
                finally:
                    release.set();app.stop.set();app.thread.join(3);app.capture_thread.join(3);app.store.db.close()

if __name__=='__main__':unittest.main()
