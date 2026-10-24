"""Actual CLI configuration must preserve independently chosen replay settings."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from foundry_loop.store import Store


class CLIConfigureTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.store = Store(Path(self.temp.name))
        self.cfg = self.store.get('config')
        self.cfg['provider'] = {'kind':'codex','command':['codex'],'model':'prior-analysis','timeout':180}
        self.cfg['replay'] = {'kind':'command','command':[sys.executable,'-c','raise SystemExit("must not run")'],'model':'prior-replay','timeout':120}
        self.store.set('config', self.cfg)

    def tearDown(self):
        self.store.db.close()
        self.temp.cleanup()

    def run_cli(self, *args, success=True):
        result = subprocess.run([sys.executable,'-m','foundry_loop','--data',self.temp.name,'configure',*args], capture_output=True,text=True,timeout=10)
        self.assertEqual(result.returncode == 0, success, result.stderr)
        return result

    def test_pi_analysis_default_preserves_replay_without_inference(self):
        result=self.run_cli('--provider','pi','--model','gpt-6.1-sol','--provider-id','openai-codex','--reasoning','medium')
        cfg=self.store.get('config')
        self.assertEqual(cfg['replay'], self.cfg['replay'])
        self.assertEqual(cfg['provider'], {'kind':'pi','command':['pi'],'model':'gpt-6.1-sol','provider':'openai-codex','reasoning':'medium','timeout':180})
        self.assertEqual(json.loads(result.stdout)['configured_target'],'analysis')
        self.assertEqual(self.store.rows('SELECT * FROM jobs'),[])

    def test_replay_only_and_explicit_both_preserve_independent_timeouts(self):
        self.run_cli('--target','replay','--provider','pi','--model','gpt-6.1-sol','--provider-id','openai-codex','--reasoning','medium')
        cfg=self.store.get('config')
        self.assertEqual(cfg['provider'],self.cfg['provider'])
        self.assertEqual(cfg['replay']['command'],['pi'])
        self.run_cli('--target','both','--provider','codex','--model','shared-model')
        cfg=self.store.get('config')
        self.assertEqual(cfg['provider']['command'],['codex'])
        self.assertEqual(cfg['replay']['command'],['codex'])
        self.assertEqual([cfg[k]['model'] for k in ('provider','replay')],['shared-model','shared-model'])
        self.assertEqual([cfg[k]['timeout'] for k in ('provider','replay')],[180,120])

    def test_command_switch_requires_explicit_command_and_errors_do_not_write(self):
        self.run_cli('--provider','command',success=False)
        self.assertEqual(self.store.get('config'),self.cfg)
        self.run_cli('--provider','pi','--reasoning','invented',success=False)
        self.assertEqual(self.store.get('config'),self.cfg)
        self.run_cli('--provider','command','--agent-command',json.dumps([sys.executable,'-c','raise SystemExit("must not run")']))
        self.assertEqual(self.store.get('config')['provider']['kind'],'command')
        self.assertEqual(self.store.get('config')['replay'],self.cfg['replay'])
