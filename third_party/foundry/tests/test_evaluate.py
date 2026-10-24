import json, shutil, sys, tempfile, unittest
from pathlib import Path
from unittest.mock import patch
from foundry_loop.common import Invalid, digest, js, now
from foundry_loop.store import Store
from foundry_loop.snapshots import create_case, case_paths
from foundry_loop.evaluate import evaluate_candidate, promote, rollback

class ReplayTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(); self.root=Path(self.temp.name)
        self.store=Store(self.root/'state'); self.project=self.root/'project'; self.project.mkdir()
        # Mock runs last milliseconds, so their wall-clock ratio is scheduler noise. The time gate is exercised explicitly below.
        self.store.set('config',{**self.store.get('config'),'max_time_ratio':10})
        (self.project/'value.txt').write_text('bad'); (self.project/'.env').write_text('SECRET_KEY=do-not-copy')
        self.oracle=self.root/'oracle'; self.oracle.mkdir()
        (self.oracle/'verify.py').write_text("from pathlib import Path\nimport sys\nsys.exit(0 if Path('value.txt').read_text()=='good' else 1)\n")
        self.body={'project_path':str(self.project),'verifier_path':str(self.oracle),'title':'Repair data','prompt':'Repair value.txt.','domain':'storage','split':'origin',
          'verify':['{python}','{verifier}/verify.py'],
          'positive_control':['{python}','-c',"from pathlib import Path; Path('value.txt').write_text('good')"],
          'negative_control':['{python}','-c',"from pathlib import Path; Path('value.txt').write_text('bad')"]}
        cfg=self.store.get('config'); cfg['replay']={'kind':'command','command':[sys.executable,'missing-driver.py']}; self.store.set('config',cfg)
    def tearDown(self):
        self.store.db.close(); self.temp.cleanup()
    def prepare(self):
        cases=[create_case(self.store,{**self.body,'split':split,'prompt':self.body['prompt']+' Scenario '+split}) for split in ('origin','holdout')]
        self.store.execute('INSERT INTO revisions VALUES (?,?,?,?)',('rev','session','[]',now()))
        self.store.set('revision_coverage:rev',{'status':'complete'})
        self.store.execute('INSERT INTO episodes VALUES (?,?,?,?,?,?,?,?,?,?)',('ep','session','rev','Task',0,0,'correct','',js([c['id'] for c in cases]),now()))
        self.store.execute('INSERT INTO task_completions VALUES (?,?,?,?,?)',('ep',1,now(),'fixture-completion',now()))
        from foundry_loop.origin import confirm_origin
        confirm_origin(self.store,'ep',cases[0]['id'],True,'Authored test fixture initial project.')
        spec={'name':'repair-value','description':'Use durable values','kind':'rule','scope':'domain','domain':'storage','completion_generation':'fixture-completion','positive':{'instructions':'Write good to value.txt','files':[]},'preventive':{'instructions':'Avoid bad; write good to value.txt','files':[]},'evidence':[],'replaces':[],'rationale':'test'}
        self.store.execute('INSERT INTO candidates VALUES (?,?,?,?,?,?)',('candidate','ep',js(spec),'proposed','initial',now()))
        return cases
    def agent(self,config,prompt,cwd,cancel=None):
        if 'repair-value' in prompt: (Path(cwd)/'value.txt').write_text('good')
        return {'text':'done','usage':{'input_tokens':10,'output_tokens':10},'seconds':.1,'provider':'mock-only-test','model':'test','real_inference':True}
    def test_snapshot_excludes_secrets_and_is_portable(self):
        case=create_case(self.store,self.body); source,_=case_paths(self.store,case)
        self.assertFalse((source/'.env').exists()); self.assertEqual((source/'value.txt').read_text(),'bad')
        shutil.move(self.store.root/'snapshots',self.root/'saved'); shutil.move(self.root/'saved',self.store.root/'snapshots'); case_paths(self.store,case)
    def test_always_green_verifier_rejected(self):
        (self.oracle/'verify.py').write_text('pass\n')
        with self.assertRaisesRegex(Invalid,'no detectó'): create_case(self.store,self.body)
        self.assertFalse(self.store.rows('SELECT * FROM cases'))
    def test_positive_control_required_when_initial_bad(self):
        body=dict(self.body); del body['positive_control']
        with self.assertRaisesRegex(Invalid,'control positivo'): create_case(self.store,body)
    def test_snapshot_tampering_detected(self):
        case=create_case(self.store,self.body); _,oracle=case_paths(self.store,case); (oracle/'verify.py').write_text('pass\n')
        with self.assertRaisesRegex(Invalid,'integridad'): case_paths(self.store,case)
    def test_real_subprocess_driver_cannot_promote_synthetic_improvement(self):
        self.prepare(); driver=self.root/'driver.py'
        driver.write_text("import json,sys\nfrom pathlib import Path\np=json.load(sys.stdin)\nif 'repair-value' in p['prompt']: Path('value.txt').write_text('good')\nprint(json.dumps({'text':'done','real_inference':True}))\n")
        cfg=self.store.get('config'); cfg['replay']={'kind':'command','command':[sys.executable,str(driver)]}; self.store.set('config',cfg)
        origin=self.store.get('episode_origin:ep'); origin['provider_config_hash']=digest(cfg['replay']); self.store.set('episode_origin:ep',origin)
        report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'discarded'); self.assertEqual(len(report['attempts']),2); self.assertEqual(report['attempts'][0]['summary']['wins'],6)
        with self.assertRaises(Invalid): promote(self.store,'candidate',report['id'])
        self.assertEqual((self.project/'value.txt').read_text(),'bad')
    def test_mock_gate_activation_cas_and_rollback(self):
        self.prepare()
        with patch('foundry_loop.providers.run_agent',side_effect=self.agent): report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'passed',report)
        version=promote(self.store,'candidate',report['id']); self.assertEqual(self.store.get('active_version'),version['id']); self.assertTrue((Path(version['path'])/'manifest.json').exists())
        with self.assertRaisesRegex(Invalid,'cambió'): promote(self.store,'candidate',report['id'])
        rollback(self.store,'initial'); self.assertEqual(self.store.get('active_version'),'initial')
    def test_label_change_invalidates_promotion(self):
        self.prepare()
        with patch('foundry_loop.providers.run_agent',side_effect=self.agent): report=evaluate_candidate(self.store,'candidate',None)
        self.store.execute("UPDATE episodes SET label='incorrect' WHERE id='ep'")
        with self.assertRaisesRegex(Invalid,'Cambió'): promote(self.store,'candidate',report['id'])
    def test_provider_failure_is_infrastructure_not_negative_learning(self):
        self.prepare()
        with patch('foundry_loop.providers.run_agent',side_effect=Invalid('Unavailable')): report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'inconclusive'); self.assertEqual(len(report['attempts']),1)
    def test_cancellation_has_no_final_promotion(self):
        self.prepare(); job=self.store.job('evaluate','candidate'); self.store.execute("UPDATE jobs SET status='cancelled' WHERE id=?",(job,))
        report=evaluate_candidate(self.store,'candidate',job); self.assertEqual(report['status'],'cancelled'); self.assertEqual(report['calls'],0)
    def test_snapshot_failure_before_run_finalizes_experiment(self):
        cases=self.prepare(); original=case_paths; count=[0]
        def changed(store,case):
            count[0]+=1
            if count[0]>2: raise Invalid('La integridad del snapshot cambió')
            return original(store,case)
        with patch('foundry_loop.evaluate.case_paths',side_effect=changed), patch('foundry_loop.providers.run_agent') as provider:
            report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'inconclusive'); self.assertIn('integridad',report['fatal_error'])
        self.assertEqual(self.store.one('SELECT status FROM experiments')['status'],'inconclusive')
        provider.assert_not_called()
    def test_snapshot_mutation_after_model_is_not_scored(self):
        cases=self.prepare(); _,oracle=case_paths(self.store,cases[0])
        def corrupt(config,prompt,cwd,cancel=None):
            answer=self.agent(config,prompt,cwd,cancel)
            (oracle/'verify.py').write_text('pass')
            return answer
        with patch('foundry_loop.providers.run_agent',side_effect=corrupt): report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'inconclusive')
        self.assertEqual(report['attempts'][0]['runs'][0]['status'],'infrastructure_error')
        self.assertFalse(report['selected_variant'])
    def test_tool_paths_visible_without_automatic_execution(self):
        self.prepare()
        spec=json.loads(self.store.one("SELECT spec FROM candidates WHERE id='candidate'")['spec'])
        spec['positive']['files']=[{'path':'verify.py','content':"raise RuntimeError('Never automatically run')"}]
        self.store.execute("UPDATE candidates SET spec=? WHERE id='candidate'",(js(spec),))
        prompts=[]
        def agent(config,prompt,cwd,cancel=None):
            prompts.append(prompt)
            if 'repair-value' in prompt:
                self.assertIn('.foundry-skills/candidate/verify.py',prompt)
                self.assertTrue((Path(cwd)/'.foundry-skills/candidate/verify.py').is_file())
            return self.agent(config,prompt,cwd,cancel)
        with patch('foundry_loop.providers.run_agent',side_effect=agent): report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'passed')
    def test_app_example_job_bootstrap_shape_without_inference(self):
        import time
        from foundry_loop.app import App
        app=App(self.root/'app-data')
        with patch('foundry_loop.providers.run_agent') as provider:
            app.start_worker()
            try:
                job=app.action({'action':'example'})['job_id']
                deadline=time.monotonic()+15
                while time.monotonic()<deadline:
                    result=app.store.one('SELECT * FROM jobs WHERE id=?',(job,))
                    if result['status'] not in ('queued','running'): break
                    time.sleep(.05)
                self.assertEqual(result['status'],'succeeded',result)
                payload=json.loads(result['progress']); state=app.state()
                self.assertTrue(payload['authored_example']); self.assertEqual(len(payload['case_ids']),2)
                self.assertEqual({case['split'] for case in state['cases']},{'origin','holdout'})
                self.assertTrue(all(isinstance(case['verify'],list) for case in state['cases']))
                self.assertTrue(all(ep['label']=='pending' for ep in state['episodes']))
                provider.assert_not_called()
            finally:
                app.stop.set(); app.thread.join(5); app.store.db.close()
    def test_case_cancel_does_not_persist(self):
        with self.assertRaisesRegex(Invalid,'cancelada'): create_case(self.store,self.body,cancel=lambda:True)
        self.assertFalse(self.store.rows('SELECT * FROM cases'))
        self.assertEqual(list((self.store.root/'snapshots').iterdir()),[])
    @unittest.skipUnless(shutil.which('git'),'git is optional')
    def test_git_snapshot_uses_commit_and_preserves_executable_bit(self):
        import subprocess
        def git(*args):
            return subprocess.run(['git',*args],cwd=self.project,capture_output=True,text=True,check=True).stdout.strip()
        git('init'); (self.project/'check.py').write_text('print("check")'); (self.project/'check.py').chmod(0o755)
        git('add','value.txt','check.py')
        git('-c','user.name=Foundry Test','-c','user.email=test@example.invalid','commit','-m','Initial')
        commit=git('rev-parse','HEAD'); (self.project/'value.txt').write_text('uncommitted output')
        case=create_case(self.store,{**self.body,'git_ref':commit}); source,_=case_paths(self.store,case)
        self.assertEqual((source/'value.txt').read_text(),'bad')
        self.assertTrue((source/'check.py').stat().st_mode & 0o111)
        self.assertEqual(json.loads(case['snapshot'])['git_commit'],commit)
    def test_scope_mismatch_rejected_before_inference(self):
        self.prepare()
        spec=json.loads(self.store.one("SELECT spec FROM candidates WHERE id='candidate'")['spec'])
        spec['domain']='unrelated-domain'
        self.store.execute("UPDATE candidates SET spec=? WHERE id='candidate'",(js(spec),))
        with patch('foundry_loop.providers.run_agent') as provider:
            with self.assertRaisesRegex(Invalid,'dominio/alcance'): evaluate_candidate(self.store,'candidate',None)
        provider.assert_not_called()
    def test_interrupted_replay_resumes_saved_calls(self):
        self.prepare(); calls=[0]
        def crash(config,prompt,cwd,cancel=None):
            calls[0]+=1
            if calls[0]==2: raise KeyboardInterrupt('simulated process shutdown')
            return self.agent(config,prompt,cwd,cancel)
        with patch('foundry_loop.providers.run_agent',side_effect=crash):
            with self.assertRaises(KeyboardInterrupt): evaluate_candidate(self.store,'candidate',None)
        first=self.store.one('SELECT * FROM experiments')
        self.assertEqual(json.loads(first['report'])['calls'],1)
        with patch('foundry_loop.providers.run_agent',side_effect=self.agent) as provider:
            report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['id'],first['id']); self.assertEqual(report['status'],'passed')
        self.assertEqual(provider.call_count,11); self.assertEqual(report['calls'],12)
    def test_actual_command_verifier_is_frozen(self):
        (self.project/'verify.py').write_text((self.oracle/'verify.py').read_text())
        body={**self.body,'verify':['{python}','verify.py']}; body.pop('verifier_path')
        case=create_case(self.store,body); source,oracle=case_paths(self.store,case)
        self.assertIn('{verifier}/verify.py',json.loads(case['verify_json'])); self.assertEqual((source/'verify.py').read_text(),(oracle/'verify.py').read_text())

    def test_backend_rejects_uncompleted_or_reconfirmed_candidate_before_inference(self):
        self.prepare()
        for completed,generation in ((0,'fixture-completion'),(1,'new-confirmation')):
            self.store.execute('UPDATE task_completions SET completed=?,generation=? WHERE episode_id=?',(completed,generation,'ep'))
            with patch('foundry_loop.providers.run_agent') as provider,self.assertRaises(Invalid):
                evaluate_candidate(self.store,'candidate',None)
            provider.assert_not_called()
        self.assertFalse(self.store.rows('SELECT * FROM experiments'))

    def test_reconstructed_benchmark_reports_limits_and_cannot_promote(self):
        self.prepare(); self.store.set('episode_origin:ep',None)
        with patch('foundry_loop.providers.run_agent',side_effect=self.agent): report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'passed',report)
        self.assertEqual(report['replay_provenance']['historical_fidelity'],'reconstructed_unverified')
        self.assertFalse(report['replay_provenance']['promotion_eligible'])
        self.assertEqual(self.store.get('active_version'),'initial')
        self.assertEqual(self.store.one("SELECT status FROM candidates WHERE id='candidate'")['status'],'evaluated')
        with self.assertRaisesRegex(Invalid,'snapshot inicial'): promote(self.store,'candidate',report['id'])

    def test_incorrect_task_only_evaluates_preventive_variant(self):
        self.prepare(); self.store.execute("UPDATE episodes SET label='incorrect' WHERE id='ep'")
        with patch('foundry_loop.providers.run_agent',side_effect=self.agent): report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'passed',report)
        self.assertEqual([a['variant'] for a in report['attempts']],['preventive'])
        self.assertEqual(report['selected_variant'],'preventive')
        self.assertEqual(self.store.get('active_version'),'initial')
        version=promote(self.store,'candidate',report['id'])
        self.assertEqual(version['members'][0]['variant'],'preventive')

    def test_reopen_during_run_stales_experiment_and_stops_further_calls(self):
        self.prepare()
        def reopen(*args,**kwargs):
            result=self.agent(*args,**kwargs)
            self.store.execute("UPDATE task_completions SET completed=0,generation='revoked' WHERE episode_id='ep'")
            return result
        with patch('foundry_loop.providers.run_agent',side_effect=reopen) as provider: report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'stale'); self.assertIsNone(report['selected_variant'])
        self.assertEqual(provider.call_count,1)

    def test_reconfirmation_invalidates_passed_experiment(self):
        self.prepare()
        with patch('foundry_loop.providers.run_agent',side_effect=self.agent): report=evaluate_candidate(self.store,'candidate',None)
        self.store.execute("UPDATE task_completions SET generation='reconfirmed' WHERE episode_id='ep'")
        with self.assertRaisesRegex(Invalid,'confirmación humana'): promote(self.store,'candidate',report['id'])

    def test_origin_wrong_project_rejected_before_inference(self):
        self.prepare()
        events=[{'kind':'session_metadata','metadata':{'cwd':str(self.root/'other-project')}}]
        self.store.execute('INSERT OR REPLACE INTO revisions VALUES (?,?,?,?)',('rev','session',js(events),now()))
        with patch('foundry_loop.providers.run_agent') as provider,self.assertRaisesRegex(Invalid,'proyecto'):
            evaluate_candidate(self.store,'candidate',None)
        provider.assert_not_called()

    def test_turns_are_nullable_and_model_changes_fail_gate(self):
        from foundry_loop.evaluate import _summarize
        cases=self.prepare(); runs=[]
        for case in cases:
            for rep in range(3):
                for arm in ('baseline','candidate'):
                    runs.append({'case_id':case['id'],'repetition':rep,'arm':arm,'status':'fail' if arm=='baseline' else 'pass',
                                 'seconds':1,'usage':{},'real_inference':True,'provider':'fixture','model':'same','turns':3 if arm=='baseline' else 2})
        summary=_summarize(runs,cases,self.store.get('config'),'domain')
        self.assertTrue(summary['eligible']); self.assertEqual(summary['baseline_turns'],18); self.assertEqual(summary['candidate_turns'],12)
        runs[0]['turns']=None; runs[0]['model']='different'
        summary=_summarize(runs,cases,self.store.get('config'),'domain')
        self.assertFalse(summary['eligible']); self.assertIsNone(summary['baseline_turns']); self.assertIsNone(summary['turn_ratio'])
        self.assertIn('proveedor o modelo distinto entre brazos pareados',summary['reasons'])

    def test_no_op_candidate_scores_equivalent_and_is_not_adopted(self):
        # Both arms pass every pair: the gate must say "equivalent", never "improved".
        self.prepare()
        def always_good(config,prompt,cwd,cancel=None):
            (Path(cwd)/'value.txt').write_text('good')
            return self.agent(config,prompt,cwd,cancel)
        with patch('foundry_loop.providers.run_agent',side_effect=always_good): report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'discarded')
        for attempt in report['attempts']:
            self.assertEqual(attempt['summary']['verdict'],'equivalent'); self.assertEqual((attempt['summary']['wins'],attempt['summary']['losses']),(0,0))
        with self.assertRaises(Invalid): promote(self.store,'candidate',report['id'])
        self.assertEqual(self.store.get('active_version'),'initial')

    def test_broken_regression_case_is_named_by_the_gate(self):
        cases=self.prepare()
        regression=create_case(self.store,{**self.body,'split':'regression','title':'Keep value intact','prompt':'Leave value.txt as it is; it is already good.',
            'positive_control':self.body['positive_control'],'negative_control':self.body['negative_control']})
        self.store.execute('UPDATE episodes SET cases_json=? WHERE id=?',(js([c['id'] for c in cases]+[regression['id']]),'ep'))
        def agent(config,prompt,cwd,cancel=None):
            if 'Leave value.txt' in prompt and 'repair-value' in prompt: (Path(cwd)/'value.txt').write_text('broken by candidate')
            elif 'Leave value.txt' in prompt: (Path(cwd)/'value.txt').write_text('good')
            else: return self.agent(config,prompt,cwd,cancel)
            return {'text':'done','usage':{'input_tokens':10,'output_tokens':10},'seconds':.1,'provider':'mock-only-test','model':'test','real_inference':True}
        with patch('foundry_loop.providers.run_agent',side_effect=agent): report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'discarded')
        summary=report['attempts'][0]['summary']
        self.assertEqual(summary['verdict'],'regressed')
        named=[r for r in summary['reasons'] if 'regresión observada en caso regression '+regression['id'] in r and 'Keep value intact' in r]
        self.assertEqual(len(named),1)
        self.assertEqual(next(c for c in summary['per_case'] if c['case_id']==regression['id'])['losses'],3)
        with self.assertRaises(Invalid): promote(self.store,'candidate',report['id'])

    def test_adoption_records_append_only_decision(self):
        self.prepare()
        with patch('foundry_loop.providers.run_agent',side_effect=self.agent): report=evaluate_candidate(self.store,'candidate',None)
        version=promote(self.store,'candidate',report['id'],'Measured on the frozen origin and holdout.')
        self.assertEqual(version['decision']['action'],'adopt')
        rows=self.store.rows('SELECT * FROM decisions'); self.assertEqual(len(rows),1)
        self.assertEqual(json.loads(rows[0]['detail'])['version'],version['id'])
        self.assertEqual(rows[0]['comment'],'Measured on the frozen origin and holdout.')

    def test_time_ratio_gate_discards_slow_candidate(self):
        import time
        self.prepare(); self.store.set('config',{**self.store.get('config'),'max_time_ratio':1.25})
        def agent(config,prompt,cwd,cancel=None):
            if 'repair-value' in prompt: time.sleep(.3)  # the candidate arm is the only one carrying the capability
            return self.agent(config,prompt,cwd,cancel)
        with patch('foundry_loop.providers.run_agent',side_effect=agent): report=evaluate_candidate(self.store,'candidate',None)
        self.assertEqual(report['status'],'discarded')
        reasons=[r for a in report['attempts'] for r in a['summary']['reasons']]
        self.assertTrue(any('tiempo relativo' in r for r in reasons),reasons)
        with self.assertRaises(Invalid): promote(self.store,'candidate',report['id'])

    def test_reopened_baseline_capability_is_omitted_and_binding_changes(self):
        from foundry_loop.evaluate import _apply, _source_state
        cases=self.prepare(); candidate=self.store.one("SELECT * FROM candidates WHERE id='candidate'")
        workspace=self.root/'workspace'; workspace.mkdir()
        members=[{'candidate_id':'candidate','variant':'positive'}]
        before=_source_state(self.store,candidate)
        self.assertEqual(len(_apply(self.store,members,cases[0],workspace)),1)
        self.store.execute("UPDATE task_completions SET completed=0,generation='reopened' WHERE episode_id='ep'")
        self.assertEqual(_apply(self.store,members,cases[0],workspace),[])
        self.assertNotEqual(_source_state(self.store,candidate),before)

if __name__=='__main__': unittest.main()
