import json
import tempfile
import unittest
from pathlib import Path
from foundry_loop.app import App,configure
from foundry_loop.common import Invalid,js,now
from foundry_loop.store import Store

class TaskAutomationTests(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.app=App(Path(self.tmp.name)/'state');self.s=self.app.store
 def tearDown(self):
  self.s.db.close();self.tmp.cleanup()
 def session(self,id='s',age=180,coverage='complete'):
  at=now()-age; rev='r'+id
  self.s.execute('INSERT INTO revisions VALUES (?,?,?,?)',(rev,id,js([{'role':'user','text':'Implement persistence'},{'role':'assistant','text':'Done'}]),at))
  self.s.execute('INSERT INTO sessions VALUES (?,?,?,?,?,?,?,?)',(id,'src','fixture','Task',rev,at,'suggested',js({'status':coverage,'activity_at':at})))
  return rev
 def enabled(self):
  self.app.action({'action':'settings','config':{'auto_tasks':True}});self.s.set('auto_tasks_since',now()-3600)
 def test_config_accepts_pi_without_changing_replay_and_rejects_bad_reasoning(self):
  old=self.s.get('config');new=configure(old,{'provider':{'kind':'pi','command':['pi'],'model':'gpt-6.1-sol','provider':'openai-codex','reasoning':'medium'}})
  self.assertEqual(new['replay'],old['replay'])
  for change in ({'auto_tasks':1},{'task_idle_seconds':1},{'provider':{'reasoning':'impossible'}}):
   with self.assertRaises(Invalid):configure(old,change)
 def test_enable_does_not_backfill_history_or_call_an_agent(self):
  self.session(); self.app.action({'action':'settings','config':{'auto_tasks':True}})
  self.assertEqual(self.app.schedule_tasks()['status'],'waiting');self.assertEqual(self.s.rows('SELECT * FROM jobs'),[])
 def test_quiet_complete_capture_queues_once_and_restart_failure_does_not_loop(self):
  rev=self.session();self.enabled();result=self.app.schedule_tasks();self.assertEqual(result['status'],'queued')
  row=self.s.one('SELECT * FROM jobs');self.assertEqual(json.loads(row['payload'])['expected_revision'],rev)
  self.assertEqual(self.app.schedule_tasks()['status'],'busy')
  self.s.execute("UPDATE jobs SET status='interrupted'")
  self.assertEqual(self.app.schedule_tasks()['status'],'waiting');self.assertEqual(len(self.s.rows('SELECT * FROM jobs')),1)
  self.assertEqual(self.s.rows('SELECT * FROM episodes'),[])
 def test_active_and_partial_capture_wait(self):
  self.session('active',age=2);self.session('partial',coverage='partial');self.enabled()
  self.assertEqual(self.app.schedule_tasks()['status'],'waiting')
 def test_manual_historical_request_is_revision_pinned_and_invalid_session_rejected(self):
  rev=self.session(age=999999);r=self.app.action({'action':'session.propose_tasks','id':'s','expected_revision':rev})
  job=self.s.one('SELECT * FROM jobs WHERE id=?',(r['job_id'],));self.assertFalse(json.loads(job['payload'])['automatic'])
  with self.assertRaises(Invalid):self.app.action({'action':'session.propose_tasks','id':'missing'})
 def test_disabling_auto_cancels_only_automatic_jobs(self):
  self.enabled();auto=self.s.job('propose_tasks','a',{'automatic':True});manual=self.s.job('propose_tasks','b',{'automatic':False})
  self.app.action({'action':'settings','config':{'auto_tasks':False}})
  self.assertEqual(self.s.one('SELECT status FROM jobs WHERE id=?',(auto,))['status'],'cancelled')
  self.assertEqual(self.s.one('SELECT status FROM jobs WHERE id=?',(manual,))['status'],'queued')
 def test_migration_preserves_existing_provider_and_does_not_enable_processing(self):
  cfg=self.s.get('config');cfg.pop('auto_tasks');cfg.pop('task_idle_seconds');cfg['provider']={'kind':'command','command':['custom'],'model':'mine','timeout':60};self.s.set('config',cfg)
  self.s.db.close();self.s=Store(Path(self.tmp.name)/'state');self.app.store=self.s
  self.assertFalse(self.s.get('config')['auto_tasks']);self.assertEqual(self.s.get('config')['provider'],cfg['provider'])

if __name__=='__main__':unittest.main()
