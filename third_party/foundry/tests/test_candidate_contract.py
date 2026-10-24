import copy,unittest
from foundry_loop.common import Invalid
from foundry_loop.learning import validate_spec

class CandidateContractTests(unittest.TestCase):
    def spec(self,kind='rule'):
        return {'name':'durable-storage','description':'Verify persistence','kind':kind,'scope':'domain','domain':'storage','positive':{'instructions':'Verify state after restart.','files':[]},'preventive':{'instructions':'Do not assume memory survives restart.','files':[]},'evidence':[{'event_index':0,'quote':'restart worked'}],'replaces':[],'rationale':'Grounded observation.'}
    def validate(self,spec):return validate_spec(spec,[{'text':'The restart worked.'}],{})
    def test_malformed_model_fields_raise_domain_error(self):
        bad=[('name',None),('name',3),('evidence','text'),('evidence',[None]),('evidence',[{}]),('evidence',[{'event_index':False,'quote':'restart worked'}]),('replaces',[{}]),('positive',[])]
        for key,value in bad:
            with self.subTest(key=key,value=value):
                spec=self.spec();spec[key]=value
                with self.assertRaises(Invalid):self.validate(spec)
    def test_malformed_file_objects_are_rejected(self):
        for value in (None,{},'text',{'path':[],'content':'x'},{'path':'a.py','content':None},{'path':'a.py','content':'x','extra':True}):
            with self.subTest(value=value):
                spec=self.spec();spec['positive']['files']=[value]
                with self.assertRaises(Invalid):self.validate(spec)
    def test_tool_requires_script_in_both_variants(self):
        spec=self.spec('tool')
        with self.assertRaisesRegex(Invalid,'script'):self.validate(spec)
        spec['positive']['files']=[{'path':'verify.py','content':"from pathlib import Path\nassert Path('data.txt').exists()\n"}]
        with self.assertRaisesRegex(Invalid,'script'):self.validate(spec)
        spec['preventive']['files']=copy.deepcopy(spec['positive']['files'])
        self.assertEqual(self.validate(spec)['kind'],'tool')
    def test_comment_only_and_invalid_scripts_are_rejected(self):
        for content in ('','# TODO: implement','pass','"documentation only"','if not valid syntax:'):
            with self.subTest(content=content):
                spec=self.spec('tool')
                for key in ('positive','preventive'):spec[key]['files']=[{'path':'verify.py','content':content}]
                with self.assertRaises(Invalid):self.validate(spec)
    def test_skill_generates_valid_frontmatter_when_missing(self):
        result=self.validate(self.spec('skill'))
        for key in ('positive','preventive'):
            self.assertTrue(result[key]['files'][0]['content'].startswith('---\nname: durable-storage\n'))
    def test_supplied_skill_frontmatter_is_required_and_checked(self):
        invalid=['# Missing header','---\nname: durable-storage\ndescription: x','---\nname: other\ndescription: x\n---','---\nname: durable-storage\n---','---\nname: durable-storage\ndescription: []\n---','---\nname: durable-storage\ndescription: null\n---','---\nname: durable-storage\ndescription: x\ndescription: y\n---']
        for content in invalid:
            with self.subTest(content=content):
                spec=self.spec('skill');spec['positive']['files']=[{'path':'SKILL.md','content':content}]
                with self.assertRaises(Invalid):self.validate(spec)
    def test_normalized_skill_path_preserves_validation(self):
        spec=self.spec('skill'); spec['positive']['files']=[{'path':'./SKILL.md','content':'---\nname: durable-storage\ndescription: Verify persistence\n---\nBody'}]
        self.assertEqual(self.validate(spec)['positive']['files'][0]['path'],'SKILL.md')
    def test_basic_plain_quoted_and_multiline_frontmatter(self):
        descriptions=['Verify persistence','"Verify persistence: after restart"',"'Verify persistence'",'>\n  Verify state\n  after restart']
        for description in descriptions:
            with self.subTest(description=description):
                spec=self.spec('skill');spec['positive']['files']=[{'path':'SKILL.md','content':'---\nname: durable-storage\ndescription: '+description+'\n---\n\nCheck the service.'}]
                self.assertEqual(self.validate(spec)['name'],'durable-storage')

if __name__=='__main__':unittest.main()
