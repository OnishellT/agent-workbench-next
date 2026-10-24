"""Action dispatcher behind the HTTP API: validates requests, runs background jobs, and keeps every promotion a human decision."""
from __future__ import annotations
import json, threading
from pathlib import Path
from .common import Invalid, clean, digest, js, now, uid
from .store import Store,DEFAULTS
from .providers import diagnostics
from .ingest import register_source, scan_sources, create_episode, _capture
from .learning import derive
from .publish import export_candidate
from .episodes import create_episode_safe

DECODE={'config':'config','coverage':'coverage','spec':'spec','report':'report','members':'members','cases_json':'cases','verify_json':'verify','negative_json':'negative'}
def decoded(row):
    result=dict(row)
    for key,target in DECODE.items():
        if key in result and isinstance(result[key],str):
            try: result[target]=json.loads(result.pop(key))
            except ValueError: pass
    return result

def configure(old,patch):
    if not isinstance(patch,dict) or set(patch)-set(DEFAULTS): raise Invalid('Configuración desconocida.')
    old={**DEFAULTS,**old};cfg={**old,**patch}
    for name in ('capture','auto_tasks','auto_derive','auto_evaluate','auto_promote'):
        if type(cfg[name])is not bool: raise Invalid('Valor booleano requerido: '+name)
    if cfg['auto_promote']: raise Invalid('La promoción requiere aprobación humana explícita; auto_promote no está permitido.')
    limits={'poll_seconds':(2,3600),'task_idle_seconds':(15,86400),'max_input_chars':(1000,500000),'repetitions':(1,10),'max_calls':(2,200),
            'experiment_seconds':(30,28800),'max_source_bytes':(1000,512000000),'max_files_per_scan':(1,1000),'max_scan_seconds':(1,120),
            'min_quality_gain':(.001,1),'max_time_ratio':(1,10),'min_efficiency_gain':(.01,.9)}
    for key,(lo,hi) in limits.items():
        if type(cfg[key])not in (int,float) or not lo<=cfg[key]<=hi: raise Invalid('Límite inválido: '+key)
    for key in ('max_input_chars','repetitions','max_calls','max_source_bytes','max_files_per_scan'):
        if type(cfg[key]) is not int: raise Invalid('El límite debe ser entero: '+key)
    for key in ('provider','replay'):
        if key in patch and not isinstance(patch[key],dict): raise Invalid('La configuración de proveedor debe ser un objeto.')
        c={**old[key],**patch.get(key,{})};cfg[key]=c
        if set(c)-{'kind','command','model','timeout','real_inference','provider','reasoning'}: raise Invalid('Campo desconocido de proveedor.')
        if c['kind']not in ('pi','codex','command'):raise Invalid('Proveedor no soportado.')
        if not isinstance(c['command'],list)or not c['command']or not all(isinstance(v,str)and v for v in c['command']):raise Invalid('Comando: usa un array JSON de argumentos.')
        if type(c['timeout'])not in(int,float)or not 5<=c['timeout']<=1800:raise Invalid('Timeout fuera de rango.')
        if not isinstance(c.get('model',''),str):raise Invalid('Modelo inválido.')
        if not isinstance(c.get('provider',''),str):raise Invalid('Proveedor del modelo inválido.')
        if c.get('reasoning','medium') not in ('off','minimal','low','medium','high','xhigh','max'):raise Invalid('Nivel de razonamiento inválido.')
        if 'real_inference'in c and type(c['real_inference'])is not bool:raise Invalid('real_inference debe ser booleano.')
    return cfg

class App:
    def __init__(self,root):
        self.store=Store(root);self.stop=threading.Event();self.thread=None;self.capture_thread=None;self.capture_lock=threading.Lock()
    def scan(self):
        with self.capture_lock:
            result=scan_sources(self.store)
            result['task_proposals']=self.schedule_tasks()
            return result
    def schedule_tasks(self):
        """Only recent quiet captures; never backfill the entire history on enable."""
        s=self.store;cfg=s.get('config')
        if not cfg.get('auto_tasks'):return {'status':'disabled'}
        if s.one("SELECT id FROM jobs WHERE kind='propose_tasks' AND status IN ('queued','running')"):
            return {'status':'busy'}
        since=s.get('auto_tasks_since')
        if since is None:
            s.set('auto_tasks_since',now());return {'status':'waiting'}
        from .task_proposals import task_proposal_status
        for session in s.rows('SELECT * FROM sessions ORDER BY updated DESC LIMIT 500'):
            coverage=json.loads(session['coverage']);activity=coverage.get('activity_at')
            if type(activity) not in (int,float) or activity<since or now()-max(activity,session['updated'])<cfg['task_idle_seconds']:continue
            if coverage.get('status')!='complete':continue
            if task_proposal_status(s,session['id'],session['revision']):continue
            # A process restart or cancellation also must not cause unattended retries.
            previous=s.rows("SELECT payload FROM jobs WHERE kind='propose_tasks' AND subject=?",(session['id'],))
            if any(json.loads(job['payload']).get('expected_revision')==session['revision'] for job in previous):continue
            job=s.job('propose_tasks',session['id'],{'expected_revision':session['revision'],'automatic':True})
            return {'status':'queued','job_id':job,'session_id':session['id']}
        return {'status':'waiting'}
    def readiness(self,id,deep=False):
        from .readiness import candidate_readiness
        return candidate_readiness(self.store,id,deep)
    def _enqueue_evaluation(self,id):
        readiness=self.readiness(id,deep=True)
        if not readiness['ready']: return {'status':'setup_required','readiness':readiness}
        return {'job_id':self.store.job('evaluate',id)}
    def state(self, compact=False):
        s=self.store
        result={}
        for key in ('sources','episodes','candidates','cases','jobs','experiments','versions'):
            # Historical job results can contain entire transcripts. The web
            # overview displays only status, not their stored result payload.
            fields=("id,kind,subject,payload,status,error,created,updated,"
                    "CASE WHEN status NOT IN ('queued','running','blocked') THEN '' "
                    "WHEN CASE WHEN json_valid(progress) THEN json_type(progress) END='object' THEN "
                    "json_object('message',substr(json_extract(progress,'$.message'),1,500),"
                    "'phase',substr(json_extract(progress,'$.phase'),1,300),"
                    "'status',substr(json_extract(progress,'$.status'),1,100),"
                    "'readiness',json_object('candidate_id',substr(json_extract(progress,'$.readiness.candidate_id'),1,200))) "
                    "ELSE substr(progress,1,2000) END AS progress,"
                    "length(progress)>2000 AS progress_truncated") if compact and key=='jobs' else '*'
            result[key]=[decoded(r) for r in s.rows('SELECT '+fields+' FROM '+key+' ORDER BY created DESC LIMIT 500')]
        result['episodes']=[s.enrich_episode(row) for row in result['episodes']]
        result['episodes']=[row for row in result['episodes'] if not row['superseded']]
        # Capture time is not conversation activity. Backfilled metadata is
        # preferred; file mtime keeps older imports discoverable during migration.
        session_fields=("sessions.id,source_id,path,title,revision,updated,completion,"
                        "json_object('status',json_extract(coverage,'$.status'),"
                        "'activity_at',json_extract(coverage,'$.activity_at'),"
                        "'event_count',json_extract(coverage,'$.event_count'),"
                        "'issue_count',json_extract(coverage,'$.issue_count')) AS coverage,"
                        "CASE WHEN proposals.value IS NOT NULL THEN json_object("
                        "'status',json_extract(proposals.value,'$.status'),"
                        "'created_count',coalesce(json_array_length(proposals.value,'$.created'),0),"
                        "'skipped_count',coalesce(json_array_length(proposals.value,'$.skipped'),0),"
                        "'progress',json_object('completed_chunks',json_extract(proposals.value,'$.progress.completed_chunks'),"
                        "'total_chunks',json_extract(proposals.value,'$.progress.total_chunks')),"
                        "'error',json_extract(proposals.value,'$.error'),"
                        "'started_at',json_extract(proposals.value,'$.started_at'),"
                        "'updated_at',json_extract(proposals.value,'$.updated_at'),"
                        "'finished_at',json_extract(proposals.value,'$.finished_at')) END AS task_proposal") if compact else '*'
        # Fetch the bounded display metadata directly: parsing every source's
        # full diagnostics on each poll is unnecessary. Session detail and the
        # default full state retain the original coverage and job results.
        session_join=" LEFT JOIN kv proposals ON proposals.key='task_proposals:'||sessions.id||':'||sessions.revision" if compact else ''
        sessions=[decoded(r) for r in s.rows('SELECT '+session_fields+' FROM sessions'+session_join)]
        task_counts={r['session_id']:r['total'] for r in s.rows("SELECT session_id,COUNT(*) AS total FROM episodes e WHERE NOT EXISTS (SELECT 1 FROM kv WHERE key='task_superseded:'||e.id) GROUP BY session_id")}
        for row in sessions:
            coverage=row.get('coverage') or {}
            activity=coverage.get('activity_at')
            if not isinstance(activity,(int,float)) or isinstance(activity,bool):
                try: activity=Path(row['path']).stat().st_mtime
                except OSError: activity=None
            row['activity_at']=activity
            row['captured_at']=row['updated']
            row['task_count']=task_counts.get(row['id'],0)
            row['task_proposal']=(json.loads(row['task_proposal']) if row['task_proposal'] else None) if compact else s.get('task_proposals:'+row['id']+':'+row['revision'])
        sessions.sort(key=lambda r:(r['activity_at'] or 0,r['updated'],r['id']),reverse=True)
        result['sessions']=sessions[:500]
        for row in result['jobs']:
            row['payload']=json.loads(row['payload'])
            if compact and row['progress'].startswith('{'):
                try: row['progress']=json.loads(row['progress'])
                except ValueError: pass
        result.update(config=s.get('config'),active_version=s.get('active_version'),diagnostics=diagnostics(s.get('config')),
                      capture_status=s.get('capture_status',{}),limits={'list_rows':500},
                      total_sessions=len(sessions))
        if compact:
            result['limits'].update(job_progress_chars=2000,state_compact=True)
        return result
    def action(self,body):
        if not isinstance(body,dict):raise Invalid('Solicitud inválida.')
        action=body.get('action');s=self.store;id=body.get('id')
        if action=='source.add':return register_source(s,body['path'],body.get('adapter','canonical'))
        if action=='source.discover':
            from .ingest import discover_sources
            return discover_sources(s)
        if action=='source.relink':
            from .ingest import relink_source
            with self.capture_lock:
                return relink_source(s,id,body['path'])
        if action=='source.toggle':
            if type(body['enabled'])is not bool:raise Invalid('enabled debe ser booleano.')
            s.execute('UPDATE sources SET enabled=? WHERE id=?',(body['enabled'],id));return {'ok':True}
        if action=='scan':return {'job_id':s.job('scan','all')}
        if action=='session.propose_tasks':
            from .episodes import _selected_revision
            _,snapshot,_=_selected_revision(s,id,body.get('expected_revision'))
            return {'job_id':s.job('propose_tasks',id,{'expected_revision':snapshot['id'],'automatic':False})}
        if action=='episode.create':return create_episode_safe(s,body['session_id'],body.get('first_event',0),body.get('last_event'),body.get('title'),body.get('expected_revision'))
        if action=='episode.origin':
            from .origin import confirm_origin
            return confirm_origin(s,id,body['case_id'],body.get('confirmed_initial_state'),body.get('note',''))
        if action=='episode.complete':
            if type(body.get('completed')) is not bool: raise Invalid('completed debe ser booleano.')
            with s.transaction():
                episode=s.one('SELECT * FROM episodes WHERE id=?',(id,))
                if not episode: raise Invalid('Episodio desconocido.')
                previous=s.task_completion(id)
                if previous['completed']==body['completed']:
                    return {**s.enrich_episode(episode),'reused':True}
                at=now(); generation=uid('completion')
                s.execute('INSERT OR REPLACE INTO task_completions VALUES (?,?,?,?,?)',
                          (id,int(body['completed']),at if body['completed'] else None,generation,at))
                if not body['completed']:
                    self._invalidate_episode(id)
                    s.execute("UPDATE episodes SET label='pending' WHERE id=?",(id,))
                    s.execute('INSERT INTO labels VALUES (?,?,?,?,?)',(uid('label'),id,'pending',episode['feedback'],at))
                s.audit('episode_completion_changed',{'id':id,'completed':body['completed'],
                        'previous_completed':previous['completed'],'previous_label':episode['label'],
                        'generation':generation,'source':'human'})
                return {**s.enrich_episode(s.one('SELECT * FROM episodes WHERE id=?',(id,))),'reused':False}
        if action=='episode.label':
            if body['label']not in('correct','incorrect','pending'):raise Invalid('Etiqueta desconocida.')
            feedback=clean(body.get('feedback',''))
            with s.transaction():
                episode=s.one('SELECT * FROM episodes WHERE id=?',(id,))
                if not episode: raise Invalid('Episodio desconocido.')
                if body['label']!='pending' and not s.task_completed(id):
                    raise Invalid('Confirma primero que la tarea está completada antes de evaluar su corrección.')
                if episode['label']==body['label'] and episode['feedback']==feedback:
                    return {'ok':True,'reused':True}
                s.execute('UPDATE episodes SET label=?,feedback=? WHERE id=?',(body['label'],feedback,id))
                s.execute('UPDATE task_completions SET generation=?,updated=? WHERE episode_id=?',
                          (uid('completion'),now(),id))
                s.execute('INSERT INTO labels VALUES (?,?,?,?,?)',(uid('label'),id,body['label'],feedback,now()))
                # Label edits revoke both pending work and prior unpromoted verdicts.
                self._invalidate_episode(id)
                s.audit('episode_label_changed',{'id':id,'label':body['label'],'previous_label':episode['label'],'source':'human'})
            if body['label']!='pending'and s.get('config')['auto_derive']:return {'job_id':s.job('derive',id)}
            return {'ok':True}
        if action=='episode.cases':
            ids=body['case_ids']
            if not isinstance(ids,list)or any(not s.one('SELECT id FROM cases WHERE id=?',(c,))for c in ids):raise Invalid('Caso desconocido.')
            s.execute('UPDATE episodes SET cases_json=? WHERE id=?',(js(list(dict.fromkeys(ids))),id));return {'ok':True}
        if action=='derive':
            self._require_derivable(id)
            return {'job_id':s.job('derive',id)}
        if action=='candidate.scope':
            row=s.one('SELECT * FROM candidates WHERE id=?',(id,))
            if not row or row['status'] in ('accepted','active','promoted','evaluating'): raise Invalid('No se puede cambiar el alcance de un candidato activo o en evaluación.')
            if body.get('scope') not in ('project','domain','general') or not isinstance(body.get('domain'),str) or not body['domain'].strip(): raise Invalid('Indica alcance y dominio válidos.')
            spec=json.loads(row['spec']);spec['domain']=clean(body['domain'].strip());spec['scope']=body['scope'];spec['fingerprint']=digest({k:spec[k] for k in ('kind','scope','domain','positive','preventive')})
            s.execute("UPDATE candidates SET spec=?,status='proposed' WHERE id=?",(js(spec),id));s.audit('candidate_scope_changed',{'id':id,'scope':spec['scope'],'domain':spec['domain']});return {'ok':True}
        if action=='candidate.evaluate':
            return self._enqueue_evaluation(id)
        if action=='candidate.setup':
            from .readiness import select_candidate_cases
            return select_candidate_cases(s,id,body.get('case_ids'))
        if action=='candidate.promote':
            from .evaluate import promote
            return promote(s,id,body['experiment_id'],body.get('comment',''))
        if action=='candidate.acknowledge_risk':
            from .miner import acknowledge_risk
            return acknowledge_risk(s,id,body.get('acknowledged'),body.get('note',''))
        if action=='candidate.reject':
            from .miner import record_decision
            with s.transaction():
                row=s.one('SELECT * FROM candidates WHERE id=?',(id,))
                if not row: raise Invalid('Candidato desconocido.')
                if row['status'] in ('accepted','active','promoted','evaluating'): raise Invalid('Cancela la evaluación o restaura una versión anterior antes de rechazar un candidato activo.')
                decision=record_decision(s,id,'reject',body.get('comment',''),{'previous_status':row['status']})
                s.execute("UPDATE candidates SET status='rejected' WHERE id=?",(id,))
                s.execute("UPDATE jobs SET status='cancelled',updated=? WHERE kind='evaluate' AND subject=? AND status IN ('queued','running')",(now(),id))
            return {'ok':True,'decision':decision}
        if action=='rollback':
            from .evaluate import rollback
            return rollback(s,body['version_id'])
        if action=='case.create':
            body=dict(body)
            if body.get('episode_id'):
                episode=s.one('SELECT revision FROM episodes WHERE id=?',(body['episode_id'],))
                if not episode: raise Invalid('La tarea para vincular el caso no existe.')
                if body.get('expected_revision') and body['expected_revision']!=episode['revision']: raise Invalid('La revisión visible cambió; abre la tarea antes de crear su caso.')
                body['expected_revision']=episode['revision']
            return {'job_id':s.job('case',uid('request'),body)}
        if action=='job.cancel':
            s.execute("UPDATE jobs SET status='cancelled',updated=? WHERE id=? AND status IN ('queued','running')",(now(),id));return {'ok':True}
        if action=='job.retry':
            row=s.one('SELECT * FROM jobs WHERE id=?',(id,))
            if not row or row['status']in('running','queued','succeeded'):raise Invalid('Este trabajo no se puede reintentar.')
            if row['kind']=='derive':self._require_derivable(row['subject'])
            if row['kind']=='evaluate':return self._enqueue_evaluation(row['subject'])
            return {'job_id':s.job(row['kind'],row['subject'],json.loads(row['payload']))}
        if action=='settings':
            old=s.get('config');cfg=configure(old,body['config'])
            with s.transaction():
                if cfg['auto_tasks'] and not old.get('auto_tasks'):s.set('auto_tasks_since',now())
                if old.get('auto_tasks') and not cfg['auto_tasks']:
                    for job in s.rows("SELECT id,payload FROM jobs WHERE kind='propose_tasks' AND status IN ('queued','running')"):
                        if json.loads(job['payload']).get('automatic'):
                            s.execute("UPDATE jobs SET status='cancelled',updated=? WHERE id=?",(now(),job['id']))
                s.set('config',cfg);s.audit('settings_changed',{'fields':list(body['config'])})
            return cfg
        if action=='backup':return {'path':s.backup()}
        if action=='example':return {'job_id':s.job('example','http-persistence')}
        if action=='export':return export_candidate(s,body['candidate_id'])
        if action=='shutdown':self.stop.set();return {'ok':True}
        raise Invalid('Acción desconocida.')
    def _require_derivable(self,id):
        episode=self.store.one('SELECT label FROM episodes WHERE id=?',(id,))
        if not episode or not self.store.task_completed(id) or episode['label'] not in ('correct','incorrect'):
            raise Invalid('Para derivar confirma la finalización y evalúa explícitamente la corrección de la tarea.')
    def _invalidate_episode(self,id):
        s=self.store
        s.execute("UPDATE candidates SET status='stale' WHERE episode_id=? AND status NOT IN ('active','promoted','accepted')",(id,))
        s.execute("UPDATE jobs SET status='cancelled',updated=? WHERE status IN ('running','queued') AND "
                  "((subject=? AND kind='derive') OR (kind='evaluate' AND subject IN (SELECT id FROM candidates WHERE episode_id=?)))",(now(),id,id))
    def example(self,job_id=None):
        from .snapshots import create_case
        s=self.store; root=Path(__file__).resolve().parent.parent/'examples'/'http-service'
        old=s.get('example')
        if old:return old
        conversation=s.root/'examples'/'http-session.jsonl';conversation.parent.mkdir(exist_ok=True)
        events=[{'id':'request','role':'user','text':'Corrige el servicio HTTP para que los registros sobrevivan al reinicio; usa DATA_FILE para su base de datos.'},
                {'id':'answer','role':'assistant','text':'Se corrigió la persistencia con SQLite y commit. La verificación hizo POST, GET, reinicio del proceso y GET otra vez. Esta conversación es un ejemplo redactado, no evidencia de una ejecución histórica.','phase':'final'}]
        conversation.write_text('\n'.join(js(e)for e in events)+'\n')
        source=register_source(s,str(conversation),'canonical')
        _capture(s,source,conversation,int(s.get('config')['max_source_bytes']))
        session=s.one('SELECT * FROM sessions WHERE source_id=?',(source['id'],))
        episode=s.one('SELECT * FROM episodes WHERE session_id=? AND revision=? ORDER BY created LIMIT 1',(session['id'],session['revision']))
        if not episode:episode=create_episode(s,session['id'],title='Ejemplo guiado · persistencia HTTP')
        case_ids=[]
        for split,title in [('origin','Persistencia después de reinicio'),('holdout','Persistencia HTTP · sobrescritura y varios registros')]:
            # Holdout exercises updates and multiple records, with an independent oracle scenario.
            body={'project_path':str(root/'project'),'verifier_path':str(root/'oracle'),'title':title,
                  'prompt':'Corrige service.py: POST /items debe guardar objetos JSON con id; GET /items/<id> debe devolverlos incluso después de reiniciar el proceso. Usa DATA_FILE para el archivo de datos y PORT para el puerto. Conserva la interfaz HTTP. Verifica tu trabajo.',
                  'domain':'python-http','split':split,'verify':['{python}','{verifier}/verify.py','{workspace}'],
                  'positive_control':['{python}','{verifier}/solution.py','{workspace}'],
                  'negative_control':['{python}','{verifier}/break.py','{workspace}']}
            if split=='holdout': body['verify'].append('--holdout')
            case=create_case(s,body,cancel=(lambda:s.cancelled(job_id)) if job_id else None);case_ids.append(case['id'])
        s.execute('UPDATE episodes SET cases_json=? WHERE id=?',(js(case_ids),episode['id']))
        result={'session_id':session['id'],'episode_id':episode['id'],'case_ids':case_ids,'authored_example':True}
        s.set('example',result);return result
    def start_worker(self):
        self.store.recover();self.thread=threading.Thread(target=self.worker,name='foundry-worker',daemon=True);self.thread.start()
        self.capture_thread=threading.Thread(target=self.capture_worker,name='foundry-capture',daemon=True);self.capture_thread.start()
    def capture_worker(self):
        while not self.stop.is_set():
            cfg=self.store.get('config')
            if cfg['capture']:
                try:self.store.set('capture_status',self.scan())
                except Exception as exc:self.store.set('capture_status',{'error':clean(str(exc))[:1000]})
            self.stop.wait(cfg['poll_seconds'])
    def worker(self):
        s=self.store
        while not self.stop.is_set():
            job=s.claim()
            if job:
                try:
                    id=job['id'];subject=job['subject'];payload=json.loads(job['payload'])
                    if job['kind']=='scan':result=self.scan()
                    elif job['kind']=='propose_tasks':
                        from .task_proposals import propose_tasks
                        if payload.get('automatic') and not s.get('config').get('auto_tasks'):
                            s.execute("UPDATE jobs SET status='cancelled',updated=? WHERE id=?",(now(),id));continue
                        result=propose_tasks(s,subject,id,payload.get('expected_revision'))
                    elif job['kind']=='derive':
                        self._require_derivable(subject)
                        result=derive(s,subject,id)
                    elif job['kind']=='evaluate':
                        from .evaluate import evaluate_candidate
                        readiness=self.readiness(subject,deep=True)
                        if not readiness['ready']:
                            with s.transaction():
                                s.execute("UPDATE jobs SET status='blocked',error=?,progress=?,updated=? WHERE id=? AND status='running'",
                                    ('Preparación pendiente: '+' '.join(b['message'] for b in readiness['blockers']),js({'status':'setup_required','readiness':readiness}),now(),id))
                            continue
                        result=evaluate_candidate(s,subject,id)
                    elif job['kind']=='case':
                        from .snapshots import create_case
                        payload['project_path']=payload.get('project_path')or payload.get('path')
                        payload['git_ref']=payload.get('git_ref')or payload.get('ref')
                        payload['negative_control']=payload.get('negative_control')or payload.get('negative')
                        result=create_case(s,payload,cancel=lambda:s.cancelled(id))
                        if payload.get('episode_id'):
                            with s.transaction():
                                episode=s.one('SELECT * FROM episodes WHERE id=?',(payload['episode_id'],))
                                if episode and episode['revision']==payload.get('expected_revision') and not s.cancelled(id):
                                    cases=list(dict.fromkeys(json.loads(episode['cases_json'])+[result['id']]))
                                    s.execute('UPDATE episodes SET cases_json=? WHERE id=?',(js(cases),episode['id']))
                                    s.audit('case.created_and_linked',{'case_id':result['id'],'episode_id':episode['id'],'revision':episode['revision']})
                                    result={**result,'linked_episode_id':episode['id']}
                                else: result={**result,'link_warning':'Caso creado pero no vinculado: la tarea/revisión cambió o se canceló el trabajo.'}
                    elif job['kind']=='example':result=self.example(id)
                    else:raise Invalid('Trabajo desconocido.')
                    with s.transaction():
                        if not s.cancelled(id):
                            s.execute("UPDATE jobs SET status='succeeded',progress=?,updated=? WHERE id=?",(js(result),now(),id))
                except Exception as exc:
                    message=clean(str(exc))[:2500]
                    s.execute("UPDATE jobs SET status='failed',error=?,updated=? WHERE id=? AND status='running'",(message,now(),job['id']))
                    s.audit('job_failed',{'id':job['id'],'error':message,'type':type(exc).__name__})
                continue
            self.stop.wait(.5)
