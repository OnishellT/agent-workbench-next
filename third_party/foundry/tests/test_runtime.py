"""Runtime integration checks: local HTTP auth, process boundaries, label fences."""
import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from urllib.error import HTTPError
from urllib.request import Request, urlopen
from unittest.mock import patch

from foundry_loop.app import App, configure
from foundry_loop.common import Invalid, js, process
from foundry_loop.ingest import register_source, scan_sources, create_episode
from foundry_loop.learning import derive
from foundry_loop.providers import run_agent
from foundry_loop.store import DEFAULTS, Store


def proposal():
    # skill-extractor mined-skill shape plus Foundry's evidence contract (miner.RESPONSE_SCHEMA).
    return {'skills':[{'name':'http-check','title':'Check the public response','category':'guardrail','description':'Check observable response',
            'trigger':'When a change claims to be done','body':'Check the response through HTTP.','trace_outcome':'success',
            'outcome_reason':'Public behavior matters.','llm_confidence':.8,'llm_utility':.7,'tags':['http'],
            'evidence':[{'quote':'Check response'}],'preventive_body':'Do not rely only on internals; inspect HTTP output.'}]}


class RuntimeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.app = App(self.root / 'state')
        self.store = self.app.store
        cfg=self.store.get('config'); cfg['auto_derive']=False; self.store.set('config',cfg)

    def tearDown(self):
        self.app.stop.set()
        self.store.db.close()
        self.temp.cleanup()

    def episode(self, partial=False):
        path=self.root/'conversation.jsonl'
        path.write_text('{"role":"user","text":"Check response"}\n' + ('{"role":' if partial else ''))
        register_source(self.store,path,'canonical');scan_sources(self.store)
        session=self.store.one('SELECT * FROM sessions')
        episode=create_episode(self.store,session['id'])
        self.app.action({'action':'episode.complete','id':episode['id'],'completed':True})
        self.app.action({'action':'episode.label','id':episode['id'],'label':'correct'})
        return episode['id']

    def job(self, episode):
        id=self.store.job('derive',episode)
        self.store.claim()
        return id

    def response(self):
        return {'text':js(proposal()),'usage':{},'seconds':.1,'provider':'command','model':'protocol-test','real_inference':False}

    def test_config_requires_integral_counts(self):
        for field in ('repetitions','max_calls','max_files_per_scan','max_source_bytes','max_input_chars'):
            with self.subTest(field=field):
                value=max(2,float(DEFAULTS[field]))+.5
                with self.assertRaises(Invalid):
                    configure(copy.deepcopy(DEFAULTS),{field:value})

    def test_config_rejects_non_object_provider_patch(self):
        for value in (None, [], 'codex'):
            with self.subTest(value=value), self.assertRaises(Invalid):
                configure(copy.deepcopy(DEFAULTS),{'provider':value})

    def test_process_fast_output_overflow_is_failure(self):
        result=process([sys.executable,'-c','print("x"*100000)'], self.root,max_output=32)
        self.assertEqual(result['error'],'output_limit')

    def test_real_subprocess_timeout_and_cancellation(self):
        script='import time; time.sleep(20)'
        started=time.monotonic()
        result=process([sys.executable,'-c',script],self.root,timeout=.15)
        self.assertEqual(result['error'],'timeout')
        self.assertLess(time.monotonic()-started,3)
        result=process([sys.executable,'-c',script],self.root,cancel=lambda:True)
        self.assertEqual(result['error'],'cancelled')

    def test_command_provider_contract_and_inference_attestation(self):
        driver=self.root/'driver.py'
        driver.write_text('import json,sys\nv=json.load(sys.stdin)\nassert v["schema_version"]==1 and v["output_schema"]=={"type":"object"}\nprint(json.dumps({"text":"{}","usage":{"input_tokens":3,"bogus":9},"model":"test","real_inference":True}))\n')
        config={'kind':'command','command':[sys.executable,str(driver)],'timeout':5}
        result=run_agent(config,'Inspect',self.root,schema={'type':'object'})
        self.assertFalse(result['real_inference'])
        self.assertEqual(result['usage'],{'input_tokens':3})
        config['real_inference']=True
        self.assertTrue(run_agent(config,'Inspect',self.root,schema={'type':'object'})['real_inference'])

    def test_command_provider_rejects_malformed_usage(self):
        config={'kind':'command','command':[sys.executable,'-c','print(\'{"text":"ok","usage":[]}\')'],'timeout':5}
        with self.assertRaises(Invalid):
            run_agent(config,'Inspect',self.root)

    def test_label_change_during_derive_prevents_save(self):
        episode=self.episode(); job=self.job(episode)
        def provider(*args,**kwargs):
            self.app.action({'action':'episode.label','id':episode,'label':'incorrect'})
            return self.response()
        with patch('foundry_loop.learning.run_agent',side_effect=provider), self.assertRaises(Invalid):
            derive(self.store,episode,job)
        self.assertEqual(self.store.rows('SELECT * FROM candidates'),[])
        self.assertEqual(self.store.one('SELECT status FROM jobs WHERE id=?',(job,))['status'],'cancelled')

    def test_duplicate_derive_still_checks_label_fence(self):
        episode=self.episode(); firstjob=self.job(episode)
        with patch('foundry_loop.learning.run_agent',return_value=self.response()):
            derive(self.store,episode,firstjob)
        self.store.execute("UPDATE jobs SET status='succeeded' WHERE id=?",(firstjob,))
        secondjob=self.job(episode)
        def provider(*args,**kwargs):
            self.app.action({'action':'episode.label','id':episode,'label':'incorrect'})
            return self.response()
        with patch('foundry_loop.learning.run_agent',side_effect=provider), self.assertRaises(Invalid):
            derive(self.store,episode,secondjob)

    def test_partial_capture_cannot_start_extraction(self):
        episode=self.episode(partial=True);job=self.job(episode)
        with patch('foundry_loop.learning.run_agent',return_value=self.response()) as provider:
            with self.assertRaises(Invalid):
                derive(self.store,episode,job)
            provider.assert_not_called()

    def test_unknown_historical_coverage_cannot_start_extraction(self):
        episode=self.episode();job=self.job(episode)
        revision=self.store.one('SELECT revision FROM episodes WHERE id=?',(episode,))['revision']
        self.store.execute('DELETE FROM kv WHERE key=?',('revision_coverage:'+revision,))
        self.store.execute('UPDATE sessions SET revision=?',('newer_revision',))
        with patch('foundry_loop.learning.run_agent',return_value=self.response()) as provider:
            with self.assertRaises(Invalid):
                derive(self.store,episode,job)
            provider.assert_not_called()

    def test_derived_candidate_evaluation_binding_matches(self):
        from foundry_loop.evaluate import _binding
        episode=self.episode();job=self.job(episode)
        with patch('foundry_loop.learning.run_agent',return_value=self.response()):
            candidate=derive(self.store,episode,job)
        binding=_binding(self.store,candidate,[])
        self.assertEqual(json.loads(candidate['spec'])['episode_binding'],binding['episode_hash'])

    def test_guided_example_runs_real_http_controls_and_stays_pending(self):
        result=self.app.example()
        self.assertTrue(result['authored_example'])
        episode=self.store.one('SELECT * FROM episodes WHERE id=?',(result['episode_id'],))
        self.assertEqual(episode['label'],'pending')
        self.assertEqual(self.store.rows('SELECT * FROM candidates'),[])
        for id in result['case_ids']:
            case=self.store.one('SELECT * FROM cases WHERE id=?',(id,))
            controls=json.loads(case['snapshot'])['controls']
            by_phase={c['phase']:c for c in controls}
            self.assertEqual(by_phase['positive_verify']['status'],'pass')
            self.assertEqual(by_phase['negative_verify']['status'],'fail')
            self.assertIn('process_restart',by_phase['positive_verify']['stdout'])
        self.assertEqual(result,self.app.example())
        self.assertEqual(len(self.store.rows('SELECT * FROM cases')),2)

    def test_job_deduplication_and_crash_recovery(self):
        id=self.store.job('scan','all')
        self.assertEqual(self.store.job('scan','all'),id)
        self.store.claim();self.store.recover()
        self.assertEqual(self.store.one('SELECT status FROM jobs WHERE id=?',(id,))['status'],'interrupted')
        new=self.store.job('scan','all')
        self.assertNotEqual(new,id)
        self.app.action({'action':'job.cancel','id':new})
        self.assertTrue(self.store.cancelled(new))


class CLIRuntimeTests(unittest.TestCase):
    def test_cli_background_lifecycle_and_configuration(self):
        with tempfile.TemporaryDirectory() as root:
            command=[sys.executable,'-m','foundry_loop','--data',root]
            def run(*args):
                result=subprocess.run(command+list(args),capture_output=True,text=True,timeout=12)
                self.assertEqual(result.returncode,0,result.stderr)
                return result.stdout
            try:
                self.assertFalse(json.loads(run('status'))['running'])
                run('configure','--provider','command','--agent-command',json.dumps([sys.executable,'-c','print("unused")']))
                doctor=json.loads(run('doctor'))
                self.assertTrue(doctor['providers']['provider']['ready'])
                run('start','--background','--port','0')
                self.assertTrue(json.loads(run('status'))['running'])
                scan=json.loads(run('call',json.dumps({'action':'scan'})))
                self.assertTrue(scan['job_id'].startswith('job_'))
                run('stop')
                deadline=time.monotonic()+5
                while time.monotonic()<deadline:
                    if not json.loads(run('status'))['running']: break
                    time.sleep(.1)
                else: self.fail('CLI did not shut down the local server')
            finally:
                subprocess.run(command+['stop'],capture_output=True,timeout=12)


class HTTPRuntimeTests(unittest.TestCase):
    def test_real_server_auth_origin_and_shutdown(self):
        with tempfile.TemporaryDirectory() as root:
            initial=Store(root);token=initial.get('token');initial.db.close()
            code='import json,sys;from foundry_loop.server import serve;serve(sys.argv[1],0,lambda m:print(json.dumps(m),flush=True))'
            child=subprocess.Popen([sys.executable,'-u','-c',code,root],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            try:
                metadata=json.loads(child.stdout.readline())
                base='http://127.0.0.1:'+str(metadata['port'])
                def call(path,headers=None,body=None):
                    request=Request(base+path,data=None if body is None else json.dumps(body).encode(),headers=headers or {})
                    try:
                        with urlopen(request,timeout=5) as response:
                            return response.status,json.load(response)
                    except HTTPError as error:
                        try: return error.code,json.load(error)
                        finally: error.close()
                self.assertEqual(call('/health')[0],200)
                self.assertEqual(call('/api/state')[0],401)
                self.assertEqual(call('/api/state',{'X-Foundry-Token':'wrong'})[0],401)
                self.assertEqual(call('/api/state',{'X-Foundry-Token':token,'Host':'evil.test'})[0],403)
                status,state=call('/api/state',{'X-Foundry-Token':token})
                self.assertEqual(status,200);self.assertNotIn('token',state)
                self.assertEqual(call('/api/action',{'X-Foundry-Token':token,'Origin':'https://evil.test'}, {'action':'scan'})[0],403)
                self.assertEqual(call('/api/action',{'X-Foundry-Token':token}, {'action':'shutdown'})[0],200)
                child.wait(timeout=7)
                self.assertEqual(child.returncode,0)
            finally:
                if child.poll() is None:
                    child.kill();child.wait()
                child.stdout.close();child.stderr.close()


if __name__=='__main__':
    unittest.main()
