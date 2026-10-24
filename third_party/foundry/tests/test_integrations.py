import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from foundry_loop.common import Invalid, digest, js, now, write_json
from foundry_loop.integrations import active_catalog, active_context, sync_project
from foundry_loop.store import Store


class IntegrationTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.root=Path(self.temp.name)
        self.store=Store(self.root/'state');self.project=self.root/'project';self.project.mkdir()
        self.store.execute('INSERT INTO episodes VALUES (?,?,?,?,?,?,?,?,?,?)',('episode_a','session_a','revision_a','Task',0,0,'correct','','[]',now()))
        self.store.execute('INSERT INTO task_completions VALUES (?,?,?,?,?)',('episode_a',1,now(),'fixture-completion',now()))
        self.next_id=1

    def tearDown(self):
        self.store.db.close();self.temp.cleanup()

    def accepted(self,name='http-check',scope='domain',domain='http',kind='rule'):
        id='candidate_'+format(self.next_id,'x');self.next_id+=1
        episode=self.store.one('SELECT * FROM episodes WHERE id=?',('episode_a',))
        spec={'name':name,'description':'Verify public behavior.','kind':kind,'scope':scope,'domain':domain,
              'positive':{'instructions':'Verify behavior through HTTP.','files':[{'path':'verify.py','content':'print("verify")\n'}]},
              'preventive':{'instructions':'Avoid internal-only assertions.','files':[]},
              'completion_generation':self.store.task_completion('episode_a')['completion_generation'],
              'episode_binding':digest({k:episode[k] for k in ('revision','label','feedback','first_event','last_event')})}
        self.store.execute('INSERT INTO candidates VALUES (?,?,?,?,?,?)',(id,'episode_a',js(spec),'accepted','initial',now()))
        return id

    def version(self,ids):
        version='version_'+format(self.next_id,'x');self.next_id+=1
        members=[{'candidate_id':id,'variant':'positive'} for id in ids]
        folder=self.store.root/'registry'/version;folder.mkdir()
        package={'schema':1,'id':version,'parent':'initial','members':members,'experiment_id':'fixture-only','artifacts':[]}
        for id in ids:
            spec=json.loads(self.store.one('SELECT spec FROM candidates WHERE id=?',(id,))['spec'])
            package['artifacts'].append({'candidate_id':id,'variant':'positive','name':spec['name'],'kind':spec['kind'],'scope':spec['scope'],'domain':spec['domain'],'content':spec['positive']})
            for file in spec['positive']['files']:
                path=folder/'artifacts'/id/file['path'];path.parent.mkdir(parents=True,exist_ok=True);path.write_text(file['content'])
        write_json(folder/'manifest.json',package)
        self.store.execute('INSERT INTO versions VALUES (?,?,?,?,?)',(version,'initial',js(members),now(),'Protocol fixture, not measured improvement'))
        self.store.audit('registry.promoted',{'version':version,'package_hash':digest(package)})
        self.store.set('active_version',version)
        return version

    def test_initial_registry_returns_empty_context_and_preserves_agents(self):
        original=b'# User rules\r\nKeep exact bytes.\n'
        (self.project/'AGENTS.md').write_bytes(original)
        self.assertEqual(active_context(self.store,'http')['capabilities'],[])
        result=sync_project(self.store,self.project,'http')
        self.assertEqual(result['installed_files'],0)
        self.assertEqual((self.project/'AGENTS.md').read_bytes(),original)

    def test_reopened_or_reconfirmed_task_does_not_load_old_capability(self):
        self.version([self.accepted()])
        self.assertEqual(len(active_context(self.store,'http')['capabilities']),1)
        self.store.execute("UPDATE task_completions SET completed=0 WHERE episode_id='episode_a'")
        self.assertEqual(active_context(self.store,'http')['capabilities'],[])
        self.store.execute("UPDATE task_completions SET completed=1,generation='new-confirmation' WHERE episode_id='episode_a'")
        self.assertEqual(active_context(self.store,'http')['capabilities'],[])

    def test_domain_general_and_project_scope(self):
        general=self.accepted('general-check','general','general')
        domain=self.accepted('http-check')
        project=self.accepted('project-check','project')
        self.store.execute('INSERT INTO cases VALUES (?,?,?,?,?,?,?,?,?)',('case_a','Origin','Task',js({'project_key':digest(str(self.project.resolve()))}),'[]','[]','http','origin',now()))
        self.store.execute('UPDATE episodes SET cases_json=?',(js(['case_a']),))
        self.version([general,domain,project])
        context=active_context(self.store,'http',self.project)
        self.assertEqual(len(context['capabilities']),3)
        for cap in context['capabilities']:
            self.assertTrue(Path(cap['files'][0]['path']).is_absolute())
        self.assertEqual(len(active_context(self.store,'http')['capabilities']),2)
        self.assertEqual(len(active_context(self.store,'other',self.project)['capabilities']),1)

    def test_sync_replace_and_rollback_preserve_user_bytes(self):
        original=b'# Human rules\r\nDo not change me.'
        (self.project/'AGENTS.md').write_bytes(original)
        first=self.accepted();self.version([first])
        result=sync_project(self.store,self.project,'http')
        self.assertEqual(result['installed_files'],2)
        self.assertTrue((self.project/'.agents/skills/foundry-http-check/SKILL.md').exists())
        self.assertTrue((self.project/'AGENTS.md').read_bytes().startswith(original))
        self.assertEqual(sync_project(self.store,self.project,'http')['installed_files'],2)
        # User edits outside the managed block remain intact through rollback.
        with (self.project/'AGENTS.md').open('ab') as f:f.write(b'\nAdditional human rule.\n')
        self.store.set('active_version','initial')
        result=sync_project(self.store,self.project,'http')
        self.assertEqual(result['removed_files'],2)
        self.assertEqual((self.project/'AGENTS.md').read_bytes(),original+b'\nAdditional human rule.\n')
        self.assertFalse((self.project/'.agents/skills/foundry-http-check').exists())

    def test_package_hash_and_file_tampering_block_use(self):
        id=self.accepted();version=self.version([id]);folder=self.store.root/'registry'/version
        path=folder/'artifacts'/id/'verify.py';path.write_text('print("tampered")')
        with self.assertRaises(Invalid):active_context(self.store,'http')
        path.write_text('print("verify")\n')
        manifest=json.loads((folder/'manifest.json').read_text());manifest['experiment_id']='tampered';write_json(folder/'manifest.json',manifest)
        with self.assertRaises(Invalid):active_context(self.store,'http')
        self.assertFalse((self.project/'AGENTS.md').exists())

    def test_foreign_files_and_user_edited_managed_files_are_not_overwritten(self):
        self.version([self.accepted()])
        foreign=self.project/'.agents/skills/foundry-http-check';foreign.mkdir(parents=True)
        (foreign/'notes.txt').write_text('Mine')
        with self.assertRaises(Invalid):sync_project(self.store,self.project,'http')
        self.assertEqual((foreign/'notes.txt').read_text(),'Mine')
        (foreign/'notes.txt').unlink();foreign.rmdir()
        sync_project(self.store,self.project,'http')
        owned=foreign/'verify.py';owned.write_text('My edited verifier')
        self.store.set('active_version','initial')
        with self.assertRaises(Invalid):sync_project(self.store,self.project,'http')
        self.assertEqual(owned.read_text(),'My edited verifier')

    def test_symlink_escape_and_edited_agents_block_are_rejected(self):
        self.version([self.accepted()])
        outside=self.root/'outside';outside.mkdir()
        (self.project/'.agents').symlink_to(outside,target_is_directory=True)
        with self.assertRaises(Invalid):sync_project(self.store,self.project,'http')
        self.assertEqual(list(outside.iterdir()),[])
        (self.project/'.agents').unlink()
        sync_project(self.store,self.project,'http')
        agents=self.project/'AGENTS.md';agents.write_text(agents.read_text().replace('Verify behavior','Human replacement'))
        with self.assertRaises(Invalid):sync_project(self.store,self.project,'http')
        self.assertIn('Human replacement',agents.read_text())

    def test_relabel_revokes_live_context_and_owned_project_artifacts(self):
        self.version([self.accepted()]);sync_project(self.store,self.project,'http')
        self.store.execute("UPDATE episodes SET label='incorrect'")
        context=active_context(self.store,'http')
        self.assertEqual(context['capabilities'],[])
        self.assertEqual(len(context['warnings']),1)
        result=sync_project(self.store,self.project,'http')
        self.assertEqual(result['removed_files'],2)
        self.assertEqual(len(result['warnings']),1)

    def test_write_failure_restores_user_files_and_allows_retry(self):
        import foundry_loop.integrations as module
        self.version([self.accepted()])
        original=b'Human rules remain.'
        (self.project/'AGENTS.md').write_bytes(original)
        actual=module._write_bytes;calls=[0]
        def interrupted(path,data):
            calls[0]+=1
            if calls[0]==3: raise OSError('simulated full disk')
            return actual(path,data)
        with patch.object(module,'_write_bytes',side_effect=interrupted), self.assertRaises(OSError):
            sync_project(self.store,self.project,'http')
        self.assertEqual((self.project/'AGENTS.md').read_bytes(),original)
        self.assertFalse((self.project/'.foundry-sync.json').exists())
        self.assertEqual(sync_project(self.store,self.project,'http')['installed_files'],2)

    def test_catalog_is_small_validated_and_revocation_aware(self):
        self.assertEqual(active_catalog(self.store)['capabilities'],[])
        general=self.accepted('general-check','general','general')
        project=self.accepted('project-check','project','http')
        self.store.execute('INSERT INTO cases VALUES (?,?,?,?,?,?,?,?,?)',('case_a','Origin','Task',js({'project_key':digest(str(self.project.resolve()))}),'[]','[]','http','origin',now()))
        self.store.execute('UPDATE episodes SET cases_json=?',(js(['case_a']),))
        self.version([general,project])
        self.assertEqual(len(active_catalog(self.store)['capabilities']),1)
        catalog=active_catalog(self.store,self.project)
        self.assertEqual(catalog['domains'],['general','http'])
        self.assertEqual(len(catalog['capabilities']),2)
        self.assertTrue(all(set(item)=={'candidate_id','name','domain','kind','scope','description'} for item in catalog['capabilities']))
        self.store.execute("UPDATE episodes SET label='incorrect'")
        catalog=active_catalog(self.store,self.project)
        self.assertEqual(catalog['capabilities'],[])
        self.assertEqual(len(catalog['warnings']),2)

    def test_catalog_cli_and_integrity_checks(self):
        import subprocess,sys
        id=self.accepted();version=self.version([id])
        result=subprocess.run([sys.executable,'-m','foundry_loop','--data',str(self.store.root),'catalog','--project',str(self.project)],capture_output=True,text=True,timeout=10)
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(json.loads(result.stdout)['capabilities'][0]['name'],'http-check')
        path=self.store.root/'registry'/version/'artifacts'/id/'verify.py'
        path.write_text('changed')
        with self.assertRaises(Invalid):active_catalog(self.store,self.project)

    def test_staged_candidate_is_never_exposed(self):
        self.accepted()
        self.assertEqual(active_context(self.store,'http')['capabilities'],[])


if __name__=='__main__':unittest.main()
