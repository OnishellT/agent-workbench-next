"""Public installer behavior: spaces, updates, relocation and data preservation."""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('installer',ROOT/'install.py')
installer=importlib.util.module_from_spec(spec); spec.loader.exec_module(installer)

class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory(); self.root=Path(self.tmp.name); self.source=self.root/'source'
        (self.source/'foundry_loop').mkdir(parents=True)
        (self.source/'foundry_loop'/'__init__.py').write_text('')
        (self.source/'foundry_loop'/'__main__.py').write_text('import json,sys\nprint(json.dumps(sys.argv[1:]))\n')
        (self.source/'README.md').write_text('v1')
    def tearDown(self): self.tmp.cleanup()
    def run_launcher(self,path,*args):
        env=os.environ.copy(); env.pop('PYTHONPATH',None)
        result=subprocess.run([sys.executable,str(path),*args],capture_output=True,text=True,env=env,cwd=self.root,check=True)
        return json.loads(result.stdout)
    def test_portable_relocation_update_uninstall(self):
        prefix=self.root/'portable with spaces'
        launch,first=installer.install(prefix,portable=True,source=self.source)
        sentinel=prefix/'var'/'conversation-foundry'/'keep.txt'; sentinel.write_text('user data')
        self.assertEqual(self.run_launcher(launch,'status'),['--data',str(sentinel.parent),'status'])
        self.assertEqual(self.run_launcher(launch,'--data','custom','status'),['--data','custom','status'])
        (self.source/'README.md').write_text('v2')
        _,second=installer.install(prefix,portable=True,source=self.source)
        self.assertNotEqual(first['release'],second['release'])
        self.assertEqual(second['previous_release'],first['release'])
        self.assertTrue((prefix/'share'/'conversation-foundry'/'releases'/first['release']).exists())
        moved=self.root/'relocated folder'; shutil.move(prefix,moved)
        shutil.rmtree(self.source)
        args=self.run_launcher(moved/'bin'/'conversation-foundry','status')
        self.assertEqual(args,['--data',str(moved/'var'/'conversation-foundry'),'status'])
        installer.uninstall(moved)
        self.assertFalse((moved/'bin'/'conversation-foundry').exists())
        self.assertFalse((moved/'share'/'conversation-foundry').exists())
        self.assertEqual((moved/'var'/'conversation-foundry'/'keep.txt').read_text(),'user data')
    def test_external_data_preserved_on_update(self):
        prefix=self.root/'standard'; data=self.root/'my data'
        installer.install(prefix,data=data,source=self.source)
        _,cfg=installer.install(prefix,source=self.source)
        self.assertEqual(cfg['data'],str(data)); installer.uninstall(prefix); self.assertTrue(data.is_dir())
    def test_ui_source_and_built_assets_ship_without_build_dependencies(self):
        for name,content in [('ui/package.json','{}'),('ui/src/main.tsx','app source'),
                             ('ui/node_modules/huge/index.js','not runtime'),
                             ('ui/dist/scratch.js','not runtime'),
                             ('foundry_loop/web/assets/main.js','compiled app')]:
            file=self.source/name;file.parent.mkdir(parents=True,exist_ok=True);file.write_text(content)
        launch,cfg=installer.install(self.root/'ui portable',portable=True,source=self.source)
        release=launch.parent.parent/'share'/'conversation-foundry'/'releases'/cfg['release']
        self.assertEqual((release/'ui/src/main.tsx').read_text(),'app source')
        self.assertEqual((release/'foundry_loop/web/assets/main.js').read_text(),'compiled app')
        self.assertFalse((release/'ui/node_modules').exists())
        self.assertFalse((release/'ui/dist').exists())
    def test_bilingual_readme_and_skill_links_survive_portable_install(self):
        (self.source/'README.md').write_text('[English](README.en.md)')
        (self.source/'README.en.md').write_text('[Español](README.md)')
        skill=self.source/'skills'/'foundry-learning-loop';skill.mkdir(parents=True)
        (skill/'SKILL.md').write_text('[Español](SKILL.es.md)')
        (skill/'SKILL.es.md').write_text('[English](SKILL.md)')
        launch,cfg=installer.install(self.root/'bilingual portable',portable=True,source=self.source)
        release=launch.parent.parent/'share'/'conversation-foundry'/'releases'/cfg['release']
        for relative in ('README.md','README.en.md','skills/foundry-learning-loop/SKILL.md','skills/foundry-learning-loop/SKILL.es.md'):
            self.assertEqual((release/relative).read_text(),(self.source/relative).read_text())
    def test_cannot_overwrite_unowned_install(self):
        prefix=self.root/'unowned'; (prefix/'bin').mkdir(parents=True)
        launch=prefix/'bin'/'conversation-foundry'; launch.write_text('some unrelated app')
        with self.assertRaises(ValueError): installer.install(prefix,portable=True,source=self.source)
        self.assertEqual(launch.read_text(),'some unrelated app')
    def test_portable_and_external_data_are_mutually_exclusive(self):
        with self.assertRaises(ValueError): installer.install(self.root/'p',portable=True,data=self.root/'data',source=self.source)

    def test_launcher_does_not_import_current_directory_shadow(self):
        launch,_=installer.install(self.root/'prefix',portable=True,source=self.source)
        shadow=self.root/'foundry_loop';shadow.mkdir()
        (shadow/'__init__.py').write_text('')
        (shadow/'__main__.py').write_text('raise SystemExit("wrong runtime from current directory")')
        self.assertEqual(self.run_launcher(launch,'status')[-1],'status')
    def test_desktop_menu_and_autostart_owned_removal(self):
        prefix=self.root/'desktop space';launcher,_=installer.install(prefix,portable=True,source=self.source)
        env={'XDG_CONFIG_HOME':str(self.root/'config'),'XDG_DATA_HOME':str(self.root/'share')}
        with mock.patch.dict(os.environ,env),mock.patch.object(installer.sys,'platform','linux'):
            desktop=installer.desktop_entry(launcher); login=installer.autostart(launcher)
            self.assertIn('start --background --open',desktop.read_text())
            self.assertNotIn('--open',login.read_text())
            self.assertTrue(installer.owns_desktop(desktop,prefix))
            other=desktop.parent/'unrelated.desktop';other.write_text('unrelated')
            installer.uninstall(prefix)
            self.assertFalse(desktop.exists());self.assertFalse(login.exists());self.assertTrue(other.exists())
    def test_unrelated_desktop_entry_never_overwritten_or_removed(self):
        prefix=self.root/'desktop';launcher,_=installer.install(prefix,portable=True,source=self.source)
        with mock.patch.dict(os.environ,{'XDG_DATA_HOME':str(self.root/'xdg')}),mock.patch.object(installer.sys,'platform','linux'):
            desktop=installer.desktop_paths()[1];desktop.parent.mkdir(parents=True);desktop.write_text('Other app')
            with self.assertRaises(ValueError): installer.desktop_entry(launcher)
            installer.uninstall(prefix);self.assertEqual(desktop.read_text(),'Other app')
    def test_restart_only_authenticated_own_service_preserves_port(self):
        responses=[subprocess.CompletedProcess([],0,json.dumps({'running':True,'server':{'port':1755}}),''),
                   subprocess.CompletedProcess([],0,'{}',''),
                   subprocess.CompletedProcess([],0,json.dumps({'running':False}),''),
                   subprocess.CompletedProcess([],0)]
        launcher=self.root/'bin'/'conversation-foundry'
        with mock.patch.object(installer.subprocess,'run',side_effect=responses) as run:
            self.assertEqual(installer.restart_server(launcher),0)
        calls=[call.args[0] for call in run.call_args_list]
        self.assertEqual([c[2] for c in calls],['status','stop','status','start'])
        self.assertEqual(calls[-1][-2:],['--port','1755'])
        self.assertTrue(all(c[:2]==[sys.executable,str(launcher)] for c in calls))
    def test_unrelated_or_failed_service_is_not_stopped(self):
        launcher=self.root/'bin'/'conversation-foundry'
        with mock.patch.object(installer.subprocess,'run',side_effect=[
            subprocess.CompletedProcess([],0,json.dumps({'running':False,'server':{'pid':123}}),''),
            subprocess.CompletedProcess([],1)]) as run:
            self.assertEqual(installer.restart_server(launcher),1)
            self.assertEqual([c.args[0][2] for c in run.call_args_list],['status','start'])
        with mock.patch.object(installer.subprocess,'run',return_value=subprocess.CompletedProcess([],1,'','failed')) as run:
            with self.assertRaises(ValueError): installer.restart_server(launcher)
            self.assertEqual(run.call_count,1)
    def test_standard_nested_data_survives_uninstall_and_reinstall(self):
        prefix=self.root/'local';data=prefix/'share'/'conversation-foundry'/'data'
        installer.install(prefix,data=data,source=self.source)
        (data/'history.sqlite').write_text('irreplaceable history')
        extra=data.parent/'unrelated.txt';extra.write_text('keep me')
        installer.uninstall(prefix)
        self.assertEqual((data/'history.sqlite').read_text(),'irreplaceable history')
        self.assertEqual(extra.read_text(),'keep me')
        self.assertFalse((data.parent/'releases').exists())
        installer.install(prefix,data=data,source=self.source)
        self.assertEqual((data/'history.sqlite').read_text(),'irreplaceable history')
    def test_failed_authenticated_stop_does_not_start_or_signal(self):
        with mock.patch.object(installer.subprocess,'run',side_effect=[
            subprocess.CompletedProcess([],0,json.dumps({'running':True,'server':{'pid':123}}),''),
            subprocess.CompletedProcess([],1,'','Unauthorized')]) as run:
            with self.assertRaises(ValueError): installer.restart_server(self.root/'bin'/'conversation-foundry')
            self.assertEqual([c.args[0][2] for c in run.call_args_list],['status','stop'])
    def test_skills_install_update_and_owned_uninstall(self):
        source=self.root/'release'/'skills'/'foundry-learning-loop';source.mkdir(parents=True)
        (source/'SKILL.md').write_text('version one')
        user=self.root/'user';(user/'.codex').mkdir(parents=True);(user/'.pi'/'agent').mkdir(parents=True)
        prefix=self.root/'skill-prefix'
        with mock.patch.object(installer.Path,'home',return_value=user):
            results=installer.install_skills(prefix,self.root/'release')
            self.assertEqual(len(results),2)
            codex=user/'.codex'/'skills'/'foundry-learning-loop'
            self.assertEqual((codex/'SKILL.md').read_text(),'version one')
            self.assertTrue(installer.owned_skill(codex,prefix))
            (source/'SKILL.md').write_text('version two')
            installer.install_skills(prefix,self.root/'release')
            self.assertEqual((codex/'SKILL.md').read_text(),'version two')
            installer.uninstall_skills(prefix)
            self.assertFalse(codex.exists());self.assertFalse((user/'.pi'/'agent'/'skills'/'foundry-learning-loop').exists())
    def test_skills_preserve_foreign_and_user_edits(self):
        source=self.root/'release'/'skills'/'foundry-learning-loop';source.mkdir(parents=True)
        (source/'SKILL.md').write_text('bundled')
        user=self.root/'user';dest=user/'.codex'/'skills'/'foundry-learning-loop';dest.mkdir(parents=True)
        (dest/'SKILL.md').write_text('foreign')
        prefix=self.root/'prefix'
        with mock.patch.object(installer.Path,'home',return_value=user):
            self.assertIn('preserved',installer.install_skills(prefix,self.root/'release')[0])
            installer.uninstall_skills(prefix);self.assertEqual((dest/'SKILL.md').read_text(),'foreign')
            shutil.rmtree(dest);installer.install_skills(prefix,self.root/'release')
            (dest/'SKILL.md').write_text('user customization')
            self.assertIn('preserved',installer.install_skills(prefix,self.root/'release')[0])
            installer.uninstall_skills(prefix);self.assertEqual((dest/'SKILL.md').read_text(),'user customization')
    def test_portable_skills_rejected_and_dry_run_never_installs(self):
        user=self.root/'user';(user/'.codex').mkdir(parents=True)
        with mock.patch.object(installer.Path,'home',return_value=user):
            self.assertEqual(installer.main(['--prefix',str(self.root/'prefix'),'--portable','--skills']),1)
            self.assertEqual(installer.main(['--prefix',str(self.root/'prefix'),'--skills','--dry-run']),0)
            self.assertFalse((user/'.codex'/'skills').exists())
    def test_dry_run_does_not_write(self):
        prefix=self.root/'preview'
        self.assertEqual(installer.main(['--prefix',str(prefix),'--portable','--dry-run','--start']),0)
        self.assertFalse(prefix.exists())

if __name__=='__main__': unittest.main()
