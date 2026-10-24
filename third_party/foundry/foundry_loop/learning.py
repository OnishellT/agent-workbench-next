"""Turn a labelled episode into candidate learnings: one bounded mining call, strict spec validation, fingerprint dedupe."""
from __future__ import annotations
import difflib, json, re, tempfile
from pathlib import Path
from .common import Invalid, clean, digest, js, now, safe_relative, uid
from .providers import run_agent

STRING={'type':'string'}
def obj(props): return {'type':'object','additionalProperties':False,'properties':props,'required':list(props)}
FILES={'type':'array','items':obj({'path':STRING,'content':STRING})}
VARIANT=obj({'instructions':STRING,'files':FILES})
SCHEMA=obj({'name':STRING,'description':STRING,'kind':{'type':'string','enum':['skill','rule','tool']},
 'scope':{'type':'string','enum':['project','domain','general']},'domain':STRING,'positive':VARIANT,'preventive':VARIANT,
 'evidence':{'type':'array','items':obj({'event_index':{'type':'integer'},'quote':STRING})},
 'replaces':{'type':'array','items':STRING},'rationale':STRING})

def validate_spec(spec,events,existing):
    if not isinstance(spec,dict) or set(spec)!=set(SCHEMA['properties']): raise Invalid('La propuesta no cumple el contrato de campos.')
    if not isinstance(spec['name'],str) or not re.fullmatch('[a-z0-9][a-z0-9-]{0,62}',spec['name']): raise Invalid('Nombre de skill inválido.')
    for key in ('description','domain','rationale'):
        if not isinstance(spec[key],str) or not spec[key].strip(): raise Invalid('Falta '+key)
        spec[key]=clean(spec[key])
    if spec['kind'] not in ('skill','rule','tool') or spec['scope'] not in ('project','domain','general'): raise Invalid('Tipo o alcance inválido.')
    if not isinstance(spec['evidence'],list) or not spec['evidence']: raise Invalid('La propuesta no incluye una lista de evidencia.')
    if not isinstance(events,list) or not isinstance(existing,dict): raise Invalid('Contexto de validación inválido.')
    for ev in spec['evidence']:
        if not isinstance(ev,dict) or set(ev)!={'event_index','quote'}: raise Invalid('Referencia de evidencia inválida.')
        i=ev['event_index']; quote=ev['quote']
        if type(i)is not int or not 0<=i<len(events) or not isinstance(quote,str) or not quote.strip(): raise Invalid('La cita no corresponde al episodio original.')
        event=events[i]
        if not isinstance(event,dict) or not isinstance(event.get('text'),str) or quote not in event['text']: raise Invalid('La cita no corresponde al episodio original.')
    if not isinstance(spec['replaces'],list) or any(not isinstance(id,str) or id not in existing for id in spec['replaces']): raise Invalid('Sustitución de capacidad desconocida.')
    if len(spec['replaces'])!=len(set(spec['replaces'])): raise Invalid('Sustitución duplicada.')

    def skill_frontmatter(content):
        lines=content.splitlines()
        if not lines or lines[0]!='---': raise Invalid('SKILL.md necesita frontmatter delimitado por ---.')
        try: end=lines.index('---',1)
        except ValueError: raise Invalid('El frontmatter de SKILL.md no está cerrado.') from None
        fields={}; index=1
        while index<end:
            line=lines[index]; index+=1
            if not line.strip() or line.lstrip().startswith('#'): continue
            match=re.fullmatch(r'([a-zA-Z_][a-zA-Z0-9_-]*):\s*(.*)',line)
            if not match:
                if line[:1].isspace(): continue
                raise Invalid('Frontmatter de SKILL.md inválido.')
            key,value=match.groups()
            if key not in ('name','description'): continue
            if key in fields: raise Invalid('Campo duplicado en el frontmatter de SKILL.md.')
            if value in ('|','>','|-','>-','|+','>+'):
                parts=[]
                while index<end and (not lines[index].strip() or lines[index][:1].isspace()):
                    parts.append(lines[index].strip());index+=1
                value=' '.join(parts).strip()
            elif value.startswith('"'):
                try: value=json.loads(value)
                except ValueError: raise Invalid('Usa un escalar entre comillas JSON válido en SKILL.md.') from None
            elif value.startswith("'"):
                if len(value)<2 or not value.endswith("'"): raise Invalid('Comillas sin cerrar en SKILL.md.')
                value=value[1:-1].replace("''", "'")
            elif ': ' in value or value.startswith(('[','{','&','*','!')) or value.lower() in ('null','~','true','false'):
                raise Invalid('name y description deben ser texto escalar en SKILL.md.')
            else: value=value.split(' #',1)[0].strip()
            if not isinstance(value,str) or not value.strip(): raise Invalid('Falta texto de '+key+' en SKILL.md.')
            fields[key]=value
        if fields.get('name')!=spec['name'] or not fields.get('description'): raise Invalid('SKILL.md necesita name coincidente y description no vacía.')

    def script_file(file):
        suffix=Path(file['path']).suffix.lower();content=file['content']
        if suffix=='.py':
            import ast
            try: tree=ast.parse(content,filename=file['path'])
            except (SyntaxError,ValueError,RecursionError): raise Invalid('El script Python de la tool no tiene sintaxis válida.') from None
            return any(not isinstance(node,ast.Pass) and not (isinstance(node,ast.Expr) and isinstance(node.value,ast.Constant)) for node in tree.body)
        if suffix in ('.sh','.bash','.js','.mjs','.cjs','.rb','.pl','.ps1'):
            return any(line.strip() and not line.lstrip().startswith(('#','//','/*','*','*/')) for line in content.splitlines())
        return False

    for key in ('positive','preventive'):
        variant=spec[key]
        if not isinstance(variant,dict) or set(variant)!= {'instructions','files'}: raise Invalid('Variante inválida.')
        if not isinstance(variant['instructions'],str) or not variant['instructions'].strip(): raise Invalid('Faltan instrucciones de '+key)
        if len(variant['instructions'])>15000: raise Invalid('La propuesta excede el tamaño de instrucciones.')
        variant['instructions']=clean(variant['instructions'])
        if not isinstance(variant['files'],list) or len(variant['files'])>12: raise Invalid('Lista de archivos inválida o demasiado extensa.')
        seen=set(); size=0
        for f in variant['files']:
            if not isinstance(f,dict) or set(f)!={'path','content'} or not isinstance(f['path'],str) or not isinstance(f['content'],str): raise Invalid('Contrato de archivo inválido.')
            if any(ord(char)<32 for char in f['path']): raise Invalid('La ruta del archivo contiene caracteres de control.')
            path=str(safe_relative(f['path'])); f['path']=path
            if path in seen: raise Invalid('Archivo duplicado o inválido.')
            seen.add(path); size+=len(f['content']); f['content']=clean(f['content'])
        if size>100000: raise Invalid('El paquete supera 100 KB.')
        if spec['kind']=='skill':
            if 'SKILL.md' not in seen:
                if len(variant['files'])>=12: raise Invalid('La skill necesita espacio para su SKILL.md dentro del límite de archivos.')
                variant['files'].insert(0,{'path':'SKILL.md','content':'---\nname: '+spec['name']+'\ndescription: '+js(spec['description'])+'\n---\n\n'+variant['instructions']+'\n'})
            skill_frontmatter(next(f['content'] for f in variant['files'] if f['path']=='SKILL.md'))
        if spec['kind']=='tool':
            scripts=[script_file(f) for f in variant['files']]
            if not any(scripts): raise Invalid('Cada variante de tool necesita un archivo de script con código ejecutable.')
        if sum(len(f['content']) for f in variant['files'])>100000: raise Invalid('El paquete completo supera 100 KB.')
    return spec

_BINDING_FIELDS=('revision','label','feedback','first_event','last_event')


def _episode_binding(episode):
    return digest({k:episode[k] for k in _BINDING_FIELDS}) if episode else None


def _learning_fence(store,episode_id,job_id,episode_hash,provider):
    current=store.one('SELECT * FROM episodes WHERE id=?',(episode_id,))
    current=store.enrich_episode(current) if current else None
    if not current or not current['completed'] or current['label'] not in ('correct','incorrect') or (job_id and store.cancelled(job_id)) or digest(current)!=episode_hash:
        raise Invalid('La etiqueta o evidencia cambió o el trabajo fue cancelado; propuesta no guardada.')
    if digest(store.get('config')['provider'])!=digest(provider):
        raise Invalid('El proveedor cambió durante la extracción; vuelve a intentarlo.')


def _mine(store,episode,job_id,events,coverage,cfg,project):
    """One bounded provider call through the vendored skill-extractor prompt.

    Budget, cancellation and label fences are Foundry's; the prompt, redaction
    and scoring are the extractor's. A completed paid call is checkpointed so a
    retry after a write failure never pays twice.
    """
    from . import miner
    provider=dict(cfg['provider']);episode_hash=digest(episode)
    limit=int(cfg['max_input_chars']); maximum=int(cfg['max_calls']); seconds=float(cfg['experiment_seconds'])
    priors,learning=miner.learning_context(store)
    prompt,receipt,signals=miner.prepare(events,episode,learning,project)
    if len(prompt)>limit:
        raise Invalid('La evidencia renderizada ('+str(len(prompt))+' caracteres) supera max_input_chars ('+str(limit)+'). Aumenta el contexto o acota la tarea; no se truncó ni omitió evidencia.')
    identity=digest({'algorithm':'skill-extractor-v1','episode_id':episode['id'],'episode_binding':_episode_binding(episode),
                     'completion_generation':episode['completion_generation'],'events':events,'coverage':coverage,
                     'provider':provider,'max_input_chars':limit,'prompt_hash':digest(prompt)})
    budget_key='learning_budget:'+identity
    budget=store.get(budget_key,{'calls':0,'seconds':0})
    deadline=now()+max(0,seconds-budget['seconds'])
    def fence():
        _learning_fence(store,episode['id'],job_id,episode_hash,provider)
    fence()
    checkpoint_key='learning_result:'+identity
    checkpoint=store.get(checkpoint_key)
    if checkpoint is None:
        with store.transaction():
            fence()
            current=store.get(budget_key,{'calls':0,'seconds':0})
            if current['calls']>=maximum or now()>=deadline:
                raise Invalid('Se agotó el presupuesto de extracción. Aumenta max_calls o experiment_seconds.')
            current['calls']+=1;store.set(budget_key,current)
        store.progress(job_id,'Minando evidencia con skill-extractor vía '+provider['kind'])
        started=now()
        try:
            with tempfile.TemporaryDirectory(prefix='foundry-extraction-') as workspace:
                selected={**provider,'timeout':min(float(provider.get('timeout',180)),max(.01,deadline-now()))}
                result=run_agent(selected,prompt,workspace,cancel=lambda:bool(job_id and store.cancelled(job_id)) or now()>=deadline or store.task_completion(episode['id'])['completion_generation']!=episode['completion_generation'],schema=miner.RESPONSE_SCHEMA)
        finally:
            with store.transaction():
                current=store.get(budget_key,{'calls':0,'seconds':0})
                current['seconds']+=max(0,now()-started);store.set(budget_key,current)
        fence()
        if now()>=deadline:raise Invalid('Se agotó el tiempo de extracción.')
        items=miner.parse_response(result['text'])
        checkpoint={'binding':identity,'prompt_hash':digest(prompt),'result':{**result,'text':js({'skills':items})}}
        checkpoint['checkpoint_hash']=digest(checkpoint)
        with store.transaction():
            fence();store.set(checkpoint_key,checkpoint)
    else:
        if checkpoint.get('binding')!=identity or checkpoint.get('prompt_hash')!=digest(prompt) or checkpoint.get('checkpoint_hash')!=digest({k:v for k,v in checkpoint.items() if k!='checkpoint_hash'}):
            raise Invalid('El checkpoint de extracción no coincide con la evidencia original.')
        result=checkpoint['result']
        items=miner.parse_response(result['text'])
    receipt.update(binding=identity,budget=store.get(budget_key),learned_from_decisions=int(priors.get('n_decisions',0)),
                   calibration_chars=len(learning),checkpoint_key=checkpoint_key)
    return result,items,receipt,signals,priors

def derive(store,episode_id,job_id):
    from . import miner
    episode=store.one('SELECT * FROM episodes WHERE id=?',(episode_id,))
    if not episode or episode['label'] not in ('correct','incorrect'): raise Invalid('Primero marca la tarea como correcta o incorrecta.')
    episode=store.enrich_episode(episode)
    if not episode['completed']: raise Invalid('Primero confirma manualmente que la tarea está completada.')
    binding=_episode_binding(episode)
    revision=store.one('SELECT events FROM revisions WHERE id=?',(episode['revision'],))
    coverage=store.get('revision_coverage:'+episode['revision'])
    if coverage is None:
        session=store.one('SELECT revision,coverage FROM sessions WHERE id=?',(episode['session_id'],))
        if session and session['revision']==episode['revision']: coverage=json.loads(session['coverage'])
    if not coverage or coverage.get('status')!='complete': raise Invalid('La captura está incompleta o su cobertura no está acreditada; revisa la fuente.')
    if not revision: raise Invalid('La revisión de evidencia ya no existe.')
    all_events=json.loads(revision['events']); events=all_events[episode['first_event']:episode['last_event']+1]
    if not events: raise Invalid('Episodio vacío.')
    if any(ev.get('incomplete') for ev in events): raise Invalid('Hay eventos incompletos: revisa la cobertura antes de aprender.')
    existing_rows=store.rows("SELECT id,spec,status FROM candidates WHERE status NOT IN ('discarded','rejected') ORDER BY created DESC LIMIT 100")
    existing={r['id']:json.loads(r['spec']) for r in existing_rows}
    case_domains=sorted({r['domain'] for cid in json.loads(episode['cases_json']) for r in store.rows('SELECT domain FROM cases WHERE id=?',(cid,))})
    from .readiness import source_directories
    directories=source_directories(store,episode['revision'])
    project=miner.lib.slugify(Path(directories[0]).name) if len(directories)==1 else 'foundry'
    domain=case_domains[0] if len(case_domains)==1 else (project if len(directories)==1 else 'unassigned')
    preferred='positive' if episode['label']=='correct' else 'preventive'
    cfg=store.get('config')
    result,items,receipt,signals,priors=_mine(store,episode,job_id,events,coverage,cfg,project)
    def discard_checkpoint(reason):
        # A response that yields nothing is not worth replaying: drop it so a retry
        # asks the provider again instead of failing forever on the cached answer.
        store.execute('DELETE FROM kv WHERE key=?',(receipt['checkpoint_key'],))
        store.audit('learning_checkpoint_discarded',{'episode':episode_id,'reason':reason})
    if not items:
        discard_checkpoint('no_skills_mined')
        raise Invalid('El minero no encontró un aprendizaje reutilizable fundamentado; no se inventó ninguno.')
    created=[];duplicates=[];rejected=[]
    base=store.get('active_version')
    for item in items:
        try: spec=miner.to_spec(item,label=episode['label'],domain=domain,events=events,existing=existing,signals=signals,priors=priors)
        except Invalid as exc:
            rejected.append({'name':str(item.get('name'))[:80],'reason':str(exc)});store.audit('candidate_rejected',{'episode':episode_id,**rejected[-1]});continue
        # Exact instruction/package equivalence avoids repeated candidates; fuzzy
        # matches remain explicit suggestions rather than automatic semantic merges.
        fingerprint=digest({k:spec[k] for k in ('kind','scope','domain','positive','preventive')})
        duplicate=next((row for row in store.rows("SELECT * FROM candidates WHERE status NOT IN ('stale','discarded','rejected')")
                        if row['episode_id']==episode_id and json.loads(row['spec']).get('fingerprint')==fingerprint
                        and json.loads(row['spec']).get('completion_generation')==episode['completion_generation'] and json.loads(row['spec']).get('episode_binding')==binding),None)
        if duplicate:
            with store.transaction(): _learning_fence(store,episode_id,job_id,digest(episode),cfg['provider'])
            store.audit('duplicate_learning',{'episode':episode_id,'candidate':duplicate['id']});duplicates.append(duplicate['id']);continue
        similar=[{'id':id,'similarity':round(difflib.SequenceMatcher(None,spec['positive']['instructions'],old['positive']['instructions']).ratio(),3)} for id,old in existing.items() if old.get('domain')==spec['domain'] and isinstance(old.get('positive'),dict)]
        spec.update(fingerprint=fingerprint,similar=sorted(similar,key=lambda x:x['similarity'],reverse=True)[:5],
                    source_revision=episode['revision'],evidence_range={'first_event':episode['first_event'],'last_event':episode['last_event']},
                    episode_binding=binding,completion_generation=episode['completion_generation'],preferred_variant=preferred,
                    analysis_coverage=receipt,provider_evidence={k:v for k,v in result.items() if k!='text'})
        id=uid('candidate')
        with store.transaction():
            _learning_fence(store,episode_id,job_id,digest(episode),cfg['provider'])
            if (job_id and store.cancelled(job_id)) or _episode_binding(store.one('SELECT * FROM episodes WHERE id=?',(episode_id,)))!=binding:
                raise Invalid('La etiqueta cambió o el trabajo fue cancelado; propuesta no guardada.')
            store.execute('INSERT INTO candidates VALUES (?,?,?,?,?,?)',(id,episode_id,js(spec),'proposed',base,now()))
            store.audit('candidate_created',{'id':id,'episode':episode_id,'risk':spec['risk']})
        created.append(id)
        if cfg['auto_evaluate'] and json.loads(episode['cases_json']): store.job('evaluate',id)
    if not created and not duplicates:
        discard_checkpoint('all_mined_skills_rejected')
        raise Invalid('Todas las skills minadas fueron rechazadas: '+'; '.join(r['reason'] for r in rejected))
    row=store.one('SELECT * FROM candidates WHERE id=?',((created or duplicates)[0],))
    return {**row,'candidate_ids':created,'duplicate_ids':duplicates,'rejected':rejected}
