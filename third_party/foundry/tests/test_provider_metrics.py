"""Provider telemetry is observed, never inferred from a final answer."""
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

from foundry_loop.providers import run_agent


class ProviderMetricsTests(unittest.TestCase):
    def test_codex_sums_completed_turns_and_separates_tools(self):
        def process(argv, cwd, **kwargs):
            Path(argv[argv.index('-o')+1]).write_text('Done')
            events=[
                {'type':'item.completed','item':{'type':'command_execution','id':'tool1'}},
                {'type':'item.completed','item':{'type':'command_execution','id':'tool1'}},
                {'type':'turn.completed','usage':{'input_tokens':100,'output_tokens':20}},
                {'type':'item.completed','item':{'type':'agent_message','id':'message'}},
                {'type':'turn.completed','usage':{'input_tokens':50,'output_tokens':10}},
            ]
            return {'stdout':'\n'.join(json.dumps(e) for e in events),'stderr':'','error':None,'returncode':0,'seconds':2}
        with tempfile.TemporaryDirectory() as root, patch('foundry_loop.providers.shutil.which',return_value='/codex'), patch('foundry_loop.providers.process',side_effect=process):
            result=run_agent({'kind':'codex'},'Task',root)
        self.assertEqual(result['turns'],2)
        self.assertEqual(result['tool_calls'],1)
        self.assertEqual(result['usage'],{'input_tokens':150,'output_tokens':30})

    def test_command_missing_metrics_are_unknown_and_invalid_are_not_zero(self):
        with tempfile.TemporaryDirectory() as root:
            for telemetry in ({},{'turns':True,'tool_calls':-1},{'turns':3,'tool_calls':2}):
                result=run_agent({'kind':'command','command':[sys.executable,'-c','print('+repr(json.dumps({'text':'Done',**telemetry}))+')']},'Task',root)
                self.assertEqual(result['turns'],3 if telemetry.get('turns')==3 else None)
                self.assertEqual(result['tool_calls'],2 if telemetry.get('tool_calls')==2 else None)
                self.assertFalse(result['real_inference'])

if __name__=='__main__': unittest.main()
