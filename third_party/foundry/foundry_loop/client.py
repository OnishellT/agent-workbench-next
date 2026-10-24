"""Small synchronous/asynchronous control surface for a persistent Python RLM."""
from __future__ import annotations
import asyncio, json, os, time, tempfile
from pathlib import Path
from .common import Invalid, clean, digest, js, now, uid

class Foundry:
    def __init__(self,data=None):
        from .__main__ import data_home
        self.data=Path(data or data_home()).expanduser().resolve()
    def state(self):
        from .__main__ import request
        return request(self.data,'/api/state')
    def call(self,action,**kwargs):
        from .__main__ import request
        return request(self.data,'/api/action',{'action':action,**kwargs})
    def wait(self,job_id,timeout=600):
        end=now()+timeout
        while now()<end:
            job=next((r for r in self.state()['jobs']if r['id']==job_id),None)
            if not job:raise Invalid('Trabajo no visible en el registro.')
            if job['status']not in('queued','running'):return job
            time.sleep(.5)
        raise TimeoutError('El trabajo sigue ejecutándose; consultar no lo cancela.')
    async def acall(self,action,**kwargs):return await asyncio.to_thread(self.call,action,**kwargs)
    async def await_job(self,job_id,timeout=600):return await asyncio.to_thread(self.wait,job_id,timeout)
    def context(self,domain,project=None):
        from .store import Store
        from .integrations import active_context
        store=Store(self.data)
        try:return active_context(store,domain,project)
        finally:store.db.close()

def run_task(store,project,domain,prompt,case_ids=()):
    """A live, explicitly requested task; initial states come from attached cases."""
    from .integrations import active_context
    from .providers import run_agent
    from .ingest import register_source,_capture,create_episode
    workspace=Path(project).expanduser().resolve()
    if not workspace.is_dir():raise Invalid('El proyecto no existe.')
    for id in case_ids:
        if not store.one('SELECT id FROM cases WHERE id=?',(id,)):raise Invalid('Caso desconocido.')
    context=active_context(store,domain,str(workspace))
    replay_config=store.get('config')['replay']
    origin=None
    id=uid('task');folder=store.root/'live';folder.mkdir(exist_ok=True)
    path=folder/(id+'.jsonl')
    def event(row):
        with path.open('a',encoding='utf8')as f:f.write(js(row)+'\n');f.flush();os.fsync(f.fileno())
    event({'id':id+'-request','role':'user','text':clean(prompt),'kind':'message'})
    source=register_source(store,str(path),'canonical')
    def capture_outcome():
        # This capture was explicitly requested by run; paused/background scan
        # limits and unrelated registered trees must not hide its outcome.
        _capture(store,source,path,int(store.get('config')['max_source_bytes']))
        session=store.one('SELECT * FROM sessions WHERE source_id=?',(source['id'],))
        episode=store.one('SELECT * FROM episodes WHERE session_id=? AND revision=? ORDER BY created LIMIT 1',(session['id'],session['revision']))
        if not episode:episode=create_episode(store,session['id'],title=clean(prompt)[:150])
        store.execute('UPDATE episodes SET cases_json=? WHERE id=?',(js(list(dict.fromkeys(case_ids))),episode['id']))
        if origin:
            store.set('episode_origin:'+episode['id'],{**origin,'source_revision':episode['revision']})
        return session,episode
    store.audit('live_task_started',{'id':id,'project':str(workspace),'version':context['version'],'case_ids':case_ids})
    # A linked case alone does not prove that it predates the task. Verify the
    # actual initial tree and prompt before the provider can mutate either.
    origin=None
    from .snapshots import copy_source, tree_hash, case_paths
    origins=[store.one("SELECT * FROM cases WHERE id=? AND split='origin'",(cid,)) for cid in case_ids]
    origins=[case for case in origins if case and case['prompt']==clean(prompt) and json.loads(case['snapshot']).get('project_key')==digest(str(workspace))]
    if origins:
        with tempfile.TemporaryDirectory(prefix='foundry-initial-') as initial:
            initial_path=Path(initial)
            copy_source(workspace,initial_path)
            initial_hash=tree_hash(initial_path)
        for case in origins:
            manifest=json.loads(case['snapshot'])
            if manifest.get('project_hash')!=initial_hash: continue
            case_paths(store,case)
            origin={'case_id':case['id'],'project_hash':initial_hash,'project_key':manifest['project_key'],
                    'prompt_hash':digest(case['prompt']),'provider_config_hash':digest(replay_config),
                    'captured_before_task':True,'provenance':'system_recorded','recorded_at':now(),
                    'baseline_version':context['version']}
            break
    try:
        result=run_agent(replay_config,prompt+'\n\nCAPACIDADES APLICABLES\n'+js(context),workspace)
        event({'id':id+'-answer','role':'assistant','text':clean(result['text']),'kind':'message','phase':'final'})
        event({'type':'task_complete','id':id+'-complete'})
    except BaseException as exc:
        event({'id':id+'-failure','role':'service','text':clean(str(exc)),'kind':'service'})
        session,episode=capture_outcome()
        store.audit('live_task_failed',{'task_id':id,'session_id':session['id'],'episode_id':episode['id'],'label':'pending'})
        raise
    session,episode=capture_outcome()
    return {'task_id':id,'session_id':session['id'],'episode_id':episode['id'],'label':'pending','version':context['version'],**result}
