import json
from pathlib import Path
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch
from foundry_loop.app import App
from foundry_loop.common import Invalid, digest, js, now
from foundry_loop.snapshots import create_case


class ReadinessTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(); self.root=Path(self.temp.name)
        self.app=App(self.root/'state'); self.store=self.app.store
        config=self.store.get('config'); config.update(capture=False,auto_derive=False)
        config['replay']={'kind':'command','command':[sys.executable,'-c','pass'],'timeout':30}
        self.store.set('config',config)
        self.project=self.root/'project'; self.project.mkdir(); (self.project/'value.txt').write_text('bad')
        self.oracle=self.root/'oracle'; self.oracle.mkdir()
        (self.oracle/'verify.py').write_text("from pathlib import Path\nimport sys\nsys.exit(0 if Path('value.txt').read_text()=='good' else 1)\n")
        events=[{'kind':'session_metadata','metadata':{'cwd':str(self.project)}},{'role':'user','text':'Repair durable storage.'}]
        self.store.execute('INSERT INTO revisions VALUES (?,?,?,?)',('revision','session',js(events),now()))
        self.store.set('revision_coverage:revision',{'status':'complete'})
        self.store.execute('INSERT INTO episodes VALUES (?,?,?,?,?,?,?,?,?,?)',('episode','session','revision','Task',1,1,'correct','','[]',now()))
        self.store.execute('INSERT INTO task_completions VALUES (?,?,?,?,?)',('episode',1,now(),'confirmed',now()))
        episode=self.store.one('SELECT * FROM episodes')
        spec={'name':'durable-storage','description':'Verify durable writes','kind':'rule','scope':'domain','domain':'storage',
              'completion_generation':'confirmed','episode_binding':digest({k:episode[k] for k in ('revision','label','feedback','first_event','last_event')}),
              'positive':{'instructions':'Check persisted writes.','files':[]},'preventive':{'instructions':'Avoid volatile writes.','files':[]},'replaces':[]}
        self.store.execute('INSERT INTO candidates VALUES (?,?,?,?,?,?)',('candidate','episode',js(spec),'proposed','initial',now()))

    def tearDown(self):
        self.app.stop.set()
        if self.app.thread:self.app.thread.join(5)
        if self.app.capture_thread:self.app.capture_thread.join(5)
        self.store.db.close(); self.temp.cleanup()

    def body(self,split):
        return {'action':'case.create','episode_id':'episode','expected_revision':'revision','project_path':str(self.project),
                'verifier_path':str(self.oracle),'title':split,'prompt':'Repair storage scenario '+split,'domain':'storage','split':split,
                'verify':['{python}','{verifier}/verify.py'],
                'positive_control':['{python}','-c',"from pathlib import Path;Path('value.txt').write_text('good')"],
                'negative_control':['{python}','-c',"from pathlib import Path;Path('value.txt').write_text('bad')"]}

    def wait(self,id):
        deadline=time.monotonic()+8
        while time.monotonic()<deadline:
            job=self.store.one('SELECT * FROM jobs WHERE id=?',(id,))
            if job['status'] not in ('running','queued'):return job
            time.sleep(.02)
        self.fail('Job did not finish')

    def test_missing_cases_returns_actionable_setup_without_failed_job(self):
        with patch('foundry_loop.providers.run_agent') as provider:
            result=self.app.action({'action':'candidate.evaluate','id':'candidate'})
        self.assertEqual(result['status'],'setup_required'); self.assertNotIn('job_id',result)
        self.assertEqual({b['code'] for b in result['readiness']['blockers']},{'missing_origin','missing_holdout'})
        self.assertEqual(result['readiness']['setup']['project_path'],str(self.project))
        self.assertEqual(result['readiness']['episode']['id'],'episode')
        self.assertEqual(result['readiness']['episode']['revision'],'revision')
        self.assertTrue(result['readiness']['episode']['completed'])
        self.assertNotIn('events',result['readiness']['episode'])
        self.assertFalse(self.store.rows('SELECT * FROM jobs')); self.assertFalse(self.store.rows('SELECT * FROM experiments'))
        provider.assert_not_called()

    def test_retry_of_production_shaped_failure_does_not_repeat_the_failure(self):
        id=self.store.job('evaluate','candidate')
        self.store.execute("UPDATE jobs SET status='failed',error=? WHERE id=?",('Vincula casos de evaluación a la tarea antes de evaluar.',id))
        result=self.app.action({'action':'job.retry','id':id})
        self.assertEqual(result['status'],'setup_required'); self.assertEqual(len(self.store.rows('SELECT * FROM jobs')),1)

    def test_legacy_queued_job_is_blocked_with_setup_receipt(self):
        id=self.store.job('evaluate','candidate')
        with patch('foundry_loop.providers.run_agent') as provider:
            self.app.start_worker(); job=self.wait(id)
        self.assertEqual(job['status'],'blocked'); self.assertIn('readiness',json.loads(job['progress']))
        provider.assert_not_called(); self.assertFalse(self.store.rows('SELECT * FROM experiments'))

    def test_case_creation_links_only_after_valid_controls_and_enables_evaluation(self):
        with patch('foundry_loop.providers.run_agent') as provider:
            self.app.start_worker()
            for split in ('origin','holdout'):
                job=self.wait(self.app.action(self.body(split))['job_id'])
                self.assertEqual(job['status'],'succeeded',job)
                self.assertEqual(json.loads(job['progress'])['linked_episode_id'],'episode')
            ready=self.app.readiness('candidate',deep=True)
        self.assertTrue(ready['ready'],ready); self.assertEqual(len(ready['selected_cases']),2)
        self.assertFalse(ready['promotion']['origin_linked']); self.assertEqual(self.store.get('active_version'),'initial')
        self.assertIsNone(self.store.get('episode_origin:episode')); provider.assert_not_called()

    def test_invalid_controls_never_attach_a_case(self):
        (self.oracle/'verify.py').write_text('pass\n'); self.app.start_worker()
        job=self.wait(self.app.action(self.body('origin'))['job_id'])
        self.assertEqual(job['status'],'failed'); self.assertEqual(self.store.one('SELECT cases_json FROM episodes')['cases_json'],'[]')

    def test_no_unrelated_case_is_suggested_or_automatically_selected(self):
        case=create_case(self.store,{**self.body('origin'),'domain':'unrelated'})
        ready=self.app.readiness('candidate')
        self.assertEqual(ready['available_cases'],[]); self.assertEqual(ready['selected_cases'],[])
        ready=self.app.action({'action':'candidate.setup','id':'candidate','case_ids':[case['id']]})
        self.assertIn('scope_mismatch',{b['code'] for b in ready['blockers']})

    def test_partial_capture_and_stale_completion_block_before_enqueue(self):
        self.store.set('revision_coverage:revision',{'status':'partial'})
        self.store.execute("UPDATE task_completions SET generation='reconfirmed' WHERE episode_id='episode'")
        ready=self.app.readiness('candidate')
        self.assertTrue({'source_incomplete','candidate_stale'}.issubset({b['code'] for b in ready['blockers']}))

    def test_unknown_capture_coverage_cannot_pass_readiness(self):
        self.store.execute("DELETE FROM kv WHERE key='revision_coverage:revision'")
        self.assertIn('source_incomplete',{b['code'] for b in self.app.readiness('candidate')['blockers']})

    def test_unrelated_holdout_cannot_pretend_to_measure_transfer(self):
        origin=create_case(self.store,self.body('origin'))
        holdout=create_case(self.store,{**self.body('holdout'),'domain':'unrelated'})
        ready=self.app.action({'action':'candidate.setup','id':'candidate','case_ids':[origin['id'],holdout['id']]})
        self.assertIn('holdout_scope_mismatch',{b['code'] for b in ready['blockers']})

    def test_public_http_readiness_and_evaluate_do_not_create_failed_job(self):
        from urllib.request import Request,urlopen
        from foundry_loop.server import serve
        state={}; ready=threading.Event()
        def started(metadata): state.update(metadata); ready.set()
        thread=threading.Thread(target=serve,args=(self.store.root,0,started),daemon=True); thread.start()
        self.assertTrue(ready.wait(5)); base='http://127.0.0.1:'+str(state['port'])
        headers={'X-Foundry-Token':self.store.get('token'),'Content-Type':'application/json'}
        def request(path,body=None):
            with urlopen(Request(base+path,headers=headers,data=js(body).encode() if body else None),timeout=5) as response:
                self.assertEqual(response.status,200); return json.load(response)
        try:
            report=request('/api/candidate/readiness?id=candidate')
            self.assertFalse(report['ready']); self.assertIn('missing_origin',{b['code'] for b in report['blockers']})
            result=request('/api/action',{'action':'candidate.evaluate','id':'candidate'})
            self.assertEqual(result['status'],'setup_required'); self.assertNotIn('job_id',result)
            self.assertFalse(self.store.rows('SELECT * FROM jobs')); self.assertFalse(self.store.rows('SELECT * FROM experiments'))
        finally:
            request('/api/action',{'action':'shutdown'}); thread.join(8)
        self.assertFalse(thread.is_alive())

    def test_design_reports_sample_size_and_budget_without_claiming_gain(self):
        cases=[create_case(self.store,self.body(split)) for split in ('origin','holdout')]
        self.app.action({'action':'candidate.setup','id':'candidate','case_ids':[c['id'] for c in cases]})
        cfg=self.store.get('config'); cfg.update(repetitions=1,max_calls=4); self.store.set('config',cfg)
        ready=self.app.readiness('candidate')
        self.assertTrue(ready['ready']); self.assertIn('insufficient_pairs',{w['code'] for w in ready['warnings']})
        self.assertFalse(ready['promotion']['possible_with_current_design']); self.assertEqual(ready['budget']['calls_first_variant'],4)
        cfg['max_calls']=2; self.store.set('config',cfg)
        self.assertIn('insufficient_budget',{b['code'] for b in self.app.readiness('candidate')['blockers']})

    def test_case_revision_mismatch_is_rejected_before_queue(self):
        with self.assertRaisesRegex(Invalid,'revisión'):
            self.app.action({**self.body('origin'),'expected_revision':'different'})
        self.assertFalse(self.store.rows('SELECT * FROM jobs'))


if __name__=='__main__':unittest.main()
