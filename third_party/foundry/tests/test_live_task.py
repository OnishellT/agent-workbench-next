import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from foundry_loop.client import run_task
from foundry_loop.common import Invalid, digest, js, now, write_json
from foundry_loop.ingest import session_detail
from foundry_loop.store import Store


class LiveTaskTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.root=Path(self.temp.name)
        self.store=Store(self.root/'state');self.project=self.root/'project';self.project.mkdir()
        self.driver=self.root/'driver.py'
        self.driver.write_text('import json,sys,pathlib\nv=json.load(sys.stdin)\np=pathlib.Path(v["workspace"])\n(p/"request.json").write_text(json.dumps(v))\n(p/"result.txt").write_text("actual mechanical filesystem change")\nprint(json.dumps({"text":"Changed result.txt","model":"mechanical-test","real_inference":False}))\n')
        cfg=self.store.get('config');cfg['replay']={'kind':'command','command':[sys.executable,str(self.driver)],'timeout':5};self.store.set('config',cfg)
        self.store.execute('INSERT INTO cases VALUES (?,?,?,?,?,?,?,?,?)',('case_a','Already registered case','Task',js({'project_key':digest(str(self.project))}),'[]','[]','http','origin',now()))

    def tearDown(self):
        self.store.db.close();self.temp.cleanup()

    def accepted(self):
        episode={'revision':'revision_a','label':'correct','feedback':'','first_event':0,'last_event':0}
        self.store.execute('INSERT INTO episodes VALUES (?,?,?,?,?,?,?,?,?,?)',('episode_evidence','session_evidence','revision_a','Evidence',0,0,'correct','','[]',now()))
        self.store.execute('INSERT INTO task_completions VALUES (?,?,?,?,?)',('episode_evidence',1,now(),'test-completion',now()))
        content={'instructions':'Inspect public HTTP behavior.','files':[]}
        spec={'name':'public-check','description':'Public checks','kind':'rule','scope':'domain','domain':'http','positive':content,'completion_generation':'test-completion','episode_binding':digest(episode)}
        self.store.execute('INSERT INTO candidates VALUES (?,?,?,?,?,?)',('candidate_a','episode_evidence',js(spec),'accepted','initial',now()))
        members=[{'candidate_id':'candidate_a','variant':'positive'}]
        package={'schema':1,'id':'version_a','parent':'initial','members':members,'experiment_id':'mechanical-fixture','artifacts':[{'candidate_id':'candidate_a','variant':'positive','name':'public-check','kind':'rule','scope':'domain','domain':'http','content':content}]}
        write_json(self.store.root/'registry'/'version_a'/'manifest.json',package)
        self.store.execute('INSERT INTO versions VALUES (?,?,?,?,?)',('version_a','initial',js(members),now(),'Protocol fixture, no measured improvement'))
        self.store.audit('registry.promoted',{'version':'version_a','package_hash':digest(package)})
        self.store.set('active_version','version_a')

    def test_real_driver_receives_prompt_and_active_context_with_one_pending_episode(self):
        self.accepted()
        result=run_task(self.store,self.project,'http','Make the requested change',['case_a'])
        request=json.loads((self.project/'request.json').read_text())
        self.assertIn('Make the requested change',request['prompt'])
        self.assertIn('Inspect public HTTP behavior.',request['prompt'])
        self.assertEqual((self.project/'result.txt').read_text(),'actual mechanical filesystem change')
        self.assertEqual(result['label'],'pending');self.assertFalse(result['real_inference'])
        episodes=self.store.rows('SELECT * FROM episodes WHERE session_id=?',(result['session_id'],))
        self.assertEqual(len(episodes),1)
        self.assertEqual(episodes[0]['label'],'pending');self.assertEqual(json.loads(episodes[0]['cases_json']),['case_a'])
        self.assertEqual(session_detail(self.store,result['session_id'])['completion'],'suggested')

    def test_explicit_run_captures_when_background_capture_is_paused(self):
        cfg=self.store.get('config');cfg['capture']=False;self.store.set('config',cfg)
        result=run_task(self.store,self.project,'http','Run explicitly',['case_a'])
        self.assertEqual(result['label'],'pending')
        self.assertEqual(session_detail(self.store,result['session_id'])['coverage']['status'],'complete')

    def test_failed_provider_is_visible_pending_with_case_links(self):
        cfg=self.store.get('config');cfg['replay']['command']=[sys.executable,'-c','import sys;print("intentional driver failure",file=sys.stderr);sys.exit(3)'];self.store.set('config',cfg)
        with self.assertRaises(Invalid):run_task(self.store,self.project,'http','Attempt the task',['case_a'])
        session=self.store.one('SELECT * FROM sessions');detail=session_detail(self.store,session['id'])
        self.assertEqual(detail['completion'],'unknown')
        self.assertTrue(any('intentional driver failure' in e['text'] for e in detail['events']))
        self.assertEqual(len(detail['episodes']),1)
        self.assertEqual(detail['episodes'][0]['label'],'pending')
        self.assertEqual(json.loads(detail['episodes'][0]['cases_json']),['case_a'])

    def test_cli_run_performs_edit_and_reports_pending(self):
        result=subprocess.run([sys.executable,'-m','foundry_loop','--data',str(self.store.root),'run','--project',str(self.project),'--domain','http','--prompt','CLI task','--case','case_a'],capture_output=True,text=True,timeout=10)
        self.assertEqual(result.returncode,0,result.stderr)
        report=json.loads(result.stdout)
        self.assertEqual(report['label'],'pending');self.assertFalse(report['real_inference'])
        self.assertTrue((self.project/'result.txt').exists())


if __name__=='__main__':unittest.main()
