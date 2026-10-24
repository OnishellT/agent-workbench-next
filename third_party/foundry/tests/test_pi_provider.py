"""Synthetic Pi protocol fixtures: these tests do not call an external model."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from foundry_loop.common import Invalid
from foundry_loop.providers import run_agent


SCHEMA={'type':'object','properties':{'ready':{'type':'boolean'}},'required':['ready'],'additionalProperties':False}


def message(text='{"ready":true}',**changes):
    return {'role':'assistant','content':[{'type':'thinking','thinking':'Private reasoning must not be returned.'},{'type':'text','text':text}],
            'model':'gpt-6.1-sol','provider':'openai-codex','thinkingLevel':'medium','stopReason':'stop',
            'usage':{'input':20,'output':7,'cacheRead':10,'cacheWrite':5,'reasoning':3,'totalTokens':42},**changes}


def stream(msg=None,extra=None,settled=True):
    msg=message() if msg is None else msg
    events=[{'type':'session','id':'synthetic'},*(extra or []),{'type':'message_end','message':msg},
            {'type':'turn_end','message':msg,'toolResults':[]},{'type':'agent_end','messages':[msg],'willRetry':False}]
    if settled: events.append({'type':'agent_settled','aborted':False})
    return '\n'.join(json.dumps(e,ensure_ascii=False) for e in events)+'\n'


class PiProviderTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(); self.cwd=Path(self.temp.name)
        (self.cwd/'AGENTS.md').write_text('These project instructions must not enter transcript analysis.')
        self.config={'kind':'pi','command':['pi'],'model':'gpt-6.1-sol','provider':'openai-codex','reasoning':'medium','timeout':30}

    def tearDown(self):self.temp.cleanup()

    def execute(self,output=None,schema=SCHEMA,process_override=None,config=None):
        response={'returncode':0,'stdout':output or stream(),'stderr':'','error':None,'seconds':.4}
        with patch('foundry_loop.providers.shutil.which',return_value='/test/pi'),patch('foundry_loop.providers.process',side_effect=process_override,return_value=response) as process:
            result=run_agent(config or self.config,'Authored synthetic prompt',self.cwd,schema=schema)
        return result,process

    def test_analysis_is_ephemeral_toolless_and_ignores_workspace_context(self):
        def process(argv,cwd,**kwargs):
            self.assertNotEqual(Path(cwd),self.cwd);self.assertEqual(list(Path(cwd).iterdir()),[])
            for flag in ('--no-session','--no-tools','--no-extensions','--no-mcp','--no-skills','--no-context-files','--no-prompt-templates','--no-themes','--no-approve','--offline'):
                self.assertIn(flag,argv)
            self.assertEqual(argv[argv.index('--thinking')+1],'medium')
            self.assertEqual(argv[argv.index('--provider')+1],'openai-codex')
            self.assertEqual(argv[argv.index('--model')+1],'gpt-6.1-sol')
            self.assertEqual(kwargs['stdin'],'Authored synthetic prompt')
            self.assertEqual(kwargs['env']['PI_TELEMETRY'],'0')
            self.assertEqual(kwargs['env']['PI_OFFLINE'],'1')
            self.assertNotIn('OPENAI_API_KEY',argv)
            self.assertEqual(kwargs['timeout'],30)
            return {'returncode':0,'stdout':stream(),'stderr':'','error':None,'seconds':.4}
        result,_=self.execute(process_override=process)
        self.assertEqual(result['text'],'{"ready":true}');self.assertTrue(result['real_inference'])
        self.assertEqual(result['structured_output'],'prompt_and_local_validation')
        self.assertEqual(result['tool_calls'],0);self.assertEqual(result['turns'],1)
        self.assertEqual(result['model_provider'],'openai-codex')
        self.assertNotIn('thinking',result['text'])

    def test_usage_is_counted_once_and_cached_input_maps_correctly(self):
        result,_=self.execute()
        self.assertEqual(result['usage'],{'input_tokens':35,'output_tokens':7,'cached_input_tokens':10,'total_tokens':42})
        self.assertIsNone(result['cost_usd']);self.assertEqual(result['reasoning'],'medium')

    def test_replay_keeps_workspace_and_builtin_tools_but_no_external_extensions(self):
        result,process=self.execute(schema=None)
        argv,cwd=process.call_args.args
        self.assertEqual(cwd,self.cwd);self.assertNotIn('--no-tools',argv)
        self.assertIn('--no-session',argv);self.assertIn('--no-extensions',argv)
        self.assertNotIn('--system-prompt',argv)

    def test_model_response_identification_overrides_requested_alias(self):
        result,_=self.execute(stream(message(responseModel='gpt-6.1-sol-actual')))
        self.assertEqual(result['model'],'gpt-6.1-sol-actual')

    def test_unicode_line_separator_is_not_a_jsonl_record_boundary(self):
        text=json.dumps({'note':'first\u2028second\u2029third'},ensure_ascii=False)
        schema={'type':'object','properties':{'note':{'type':'string'}},'required':['note'],'additionalProperties':False}
        result,_=self.execute(stream(message(text)),schema=schema)
        self.assertEqual(json.loads(result['text'])['note'],'first\u2028second\u2029third')

    def test_exit_zero_does_not_hide_authentication_error(self):
        with self.assertRaisesRegex(Invalid,'Please log in'):
            self.execute(stream(message('',stopReason='error',errorMessage='No API key found. Please log in.')))

    def test_incomplete_or_truncated_final_is_rejected(self):
        for output in (stream(settled=False),stream(message(stopReason='length')),stream(message(stopReason='aborted'))):
            with self.subTest(output=output[-80:]),self.assertRaises(Invalid):self.execute(output)

    def test_schema_rejects_extra_fields_wrong_types_and_markdown(self):
        for text in ('{"ready":1}','{"ready":true,"extra":"not allowed"}','{}','```json\n{"ready":true}\n```'):
            with self.subTest(text=text),self.assertRaises(Invalid):self.execute(stream(message(text)))

    def test_schema_unknown_constraints_fail_instead_of_false_validation(self):
        with self.assertRaisesRegex(Invalid,'no soportado'):self.execute(schema={'$ref':'#/definitions/Result'})

    def test_tool_attempt_during_analysis_is_rejected(self):
        with self.assertRaisesRegex(Invalid,'herramientas'):
            self.execute(stream(extra=[{'type':'tool_execution_start','toolCallId':'synthetic-call','toolName':'bash'}]))

    def test_retry_final_success_uses_final_text_and_accounts_for_prior_usage(self):
        earlier=message('',stopReason='error',errorMessage='temporary connection issue')
        result,_=self.execute(stream(extra=[{'type':'message_end','message':earlier},{'type':'turn_end','message':earlier},{'type':'auto_retry_end','success':True}]))
        self.assertEqual(result['text'],'{"ready":true}');self.assertEqual(result['usage']['total_tokens'],84)
        self.assertEqual(result['turns'],2)

    def test_inline_session_or_extension_flags_are_not_accepted(self):
        for command in (['pi','--continue'],['pi','--extension','untrusted.js']):
            with self.assertRaisesRegex(Invalid,'ejecutable'):self.execute(config={**self.config,'command':command})

    def test_pi_requires_explicit_model_and_valid_reasoning(self):
        for config in ({**self.config,'model':''},{**self.config,'reasoning':'unknown'}):
            with self.assertRaises(Invalid):self.execute(config=config)

    def test_process_timeout_or_cancellation_is_not_success(self):
        for error in ('timeout','cancelled'):
            def process(*args,**kwargs):return {'returncode':-9,'stdout':stream(),'stderr':'','error':error,'seconds':.1}
            with self.assertRaisesRegex(Invalid,error):self.execute(process_override=process)


if __name__=='__main__':unittest.main()
