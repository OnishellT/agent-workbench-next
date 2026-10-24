"""Paired replay, evidence-bound promotion and reversible registry versions."""
from __future__ import annotations
import json, math, random, shutil, tempfile
from pathlib import Path
from .common import Invalid, clean, digest, js, now, safe_relative, uid, write_json
from .snapshots import case_paths, verifier_run


def _candidate(store,id):
    row=store.one('SELECT * FROM candidates WHERE id=?',(id,))
    if not row: raise Invalid('Candidato inexistente.')
    return row,json.loads(row['spec'])

def _source_state(store,candidate):
    ep=store.one('SELECT * FROM episodes WHERE id=?',(candidate['episode_id'],))
    return {'episode':{k:ep[k] for k in ('revision','label','feedback','first_event','last_event')} if ep else None,
            'completion':store.task_completion(candidate['episode_id'])}

def _binding(store,candidate,cases):
    ep=store.one('SELECT * FROM episodes WHERE id=?',(candidate['episode_id'],))
    if not ep or ep['label'] not in ('correct','incorrect'): raise Invalid('La tarea necesita una etiqueta correcta o incorrecta antes de evaluar.')
    completion=store.task_completion(ep['id'])
    if not completion['completed']: raise Invalid('La tarea debe estar completada por confirmación humana antes de evaluar o promover.')
    episode_hash=digest({k:ep[k] for k in ('revision','label','feedback','first_event','last_event')})
    spec=json.loads(candidate['spec'])
    if not spec.get('completion_generation') or spec['completion_generation']!=completion['completion_generation']: raise Invalid('Cambió la confirmación humana de la tarea. Genera de nuevo el aprendizaje.')
    if spec.get('episode_binding') and spec['episode_binding']!=episode_hash: raise Invalid('La etiqueta o evidencia cambió después de generar el candidato.')
    labels=store.rows('SELECT * FROM labels WHERE episode_id=? ORDER BY created,id',(ep['id'],))
    return {'candidate_hash':digest(candidate['spec']),'episode_hash':episode_hash,'completion':completion,'origin':store.get('episode_origin:'+ep['id']),'selection_hash':digest(ep['cases_json']),'labels_hash':digest(labels),'case_hashes':{c['id']:digest(c) for c in cases},'config_hash':digest(store.get('config')),'baseline_hash':digest([{'member':m,'spec':row['spec'],'source_state':_source_state(store,row)} for m in _members(store,store.get('active_version')) for row in [_candidate(store,m['candidate_id'])[0]]])}

def _replay_provenance(store,episode,cases,config):
    """Distinguish paired reproducibility from evidence about the historical start.

    An explicitly confirmed initial snapshot is an attestation, not something
    that can be inferred from a conversation's final working tree.
    """
    origin=store.get('episode_origin:'+episode['id'])
    origins=[case for case in cases if case['split']=='origin']
    result={'paired_conditions':'Both arms start from the same frozen case tree, prompt, verifier, replay configuration and baseline harness.',
            'historical_fidelity':'reconstructed_unverified','promotion_eligible':False,
            'reason':'Falta un snapshot inicial vinculado a la tarea; el benchmark reconstruido no acredita las condiciones históricas.',
            'origin':origin,'cases':[{'case_id':c['id'],'split':c['split'],'project_hash':json.loads(c['snapshot'])['project_hash'],'prompt_hash':digest(c['prompt'])} for c in cases]}
    # Recorded session cwd is useful evidence of project identity, never of its
    # historical contents. Read only known metadata shapes; do not guess paths
    # from prose, arbitrary tool output or instruction-like native events.
    from .readiness import source_directories
    keys={digest(str(Path(cwd).resolve())) for cwd in source_directories(store,episode['revision'])}
    if keys and any(json.loads(c['snapshot']).get('project_key') not in keys for c in origins):
        raise Invalid('El proyecto del caso de origen no coincide con el directorio registrado en la conversación.')
    if origin is None: return result
    if not isinstance(origin,dict): raise Invalid('El vínculo del snapshot de origen es inválido.')
    case=next((c for c in origins if c['id']==origin.get('case_id')),None)
    if not case: raise Invalid('El snapshot inicial vinculado no está entre los casos de origen seleccionados.')
    manifest=json.loads(case['snapshot'])
    expected={'source_revision':episode['revision'],'project_hash':manifest['project_hash'],'project_key':manifest.get('project_key'),
              'prompt_hash':digest(case['prompt']),'provider_config_hash':digest(config['replay'])}
    if any(origin.get(k)!=v for k,v in expected.items()): raise Invalid('Cambió la revisión, snapshot, instrucciones o proveedor respecto al origen vinculado.')
    if origin.get('captured_before_task') is not True or origin.get('provenance') not in ('system_recorded','human_attested'):
        raise Invalid('El origen requiere captura inicial registrada o confirmación humana explícita.')
    result.update(historical_fidelity=origin['provenance'],promotion_eligible=True,
                  reason='Snapshot inicial registrado por el sistema.' if origin['provenance']=='system_recorded' else 'El usuario declara que el snapshot corresponde al inicio; su equivalencia histórica no fue comprobada automáticamente.')
    return result

def _members(store,version):
    row=store.one('SELECT * FROM versions WHERE id=?',(version,))
    if not row: raise Invalid('Versión del arnés inexistente.')
    members=json.loads(row['members'])
    if version!='initial':
        folder=store.root/'registry'/version
        try: package=json.loads((folder/'manifest.json').read_text())
        except (OSError,ValueError): raise Invalid('El paquete de la versión falta o está dañado.') from None
        if package.get('members')!=members: raise Invalid('La versión no coincide con su paquete inmutable.')
        for artifact in package.get('artifacts',[]):
            _,spec=_candidate(store,artifact['candidate_id'])
            if spec.get(artifact['variant'])!=artifact['content']: raise Invalid('La capacidad aceptada cambió después de su evaluación.')
            for file in artifact['content'].get('files',[]):
                path=folder/'artifacts'/artifact['candidate_id']/safe_relative(file['path'])
                if not path.is_file() or path.read_text()!=file['content']: raise Invalid('Un archivo del registro cambió después de su evaluación.')
    return [m if isinstance(m,dict) else {'candidate_id':m,'variant':'positive'} for m in members]

def _applicable(store,candidate,spec,case):
    if spec.get('scope')=='general': return True
    if spec.get('domain')!=case['domain']: return False
    if spec.get('scope')!='project': return True
    episode=store.one('SELECT cases_json FROM episodes WHERE id=?',(candidate['episode_id'],))
    keys=set()
    for id in json.loads(episode['cases_json']) if episode else []:
        origin=store.one("SELECT snapshot FROM cases WHERE id=? AND split='origin'",(id,))
        if origin: keys.add(json.loads(origin['snapshot']).get('project_key'))
    return json.loads(case['snapshot']).get('project_key') in keys-{None}

def _apply(store,members,case,workspace):
    context=[]
    for member in members:
        candidate,spec=_candidate(store,member['candidate_id'])
        state=_source_state(store,candidate); episode=state['episode']; completion=state['completion']
        if not episode or not completion['completed'] or episode['label'] not in ('correct','incorrect') or spec.get('completion_generation')!=completion['completion_generation']: continue
        if spec.get('episode_binding') and spec['episode_binding']!=digest(episode): continue
        if not _applicable(store,candidate,spec,case): continue
        variant=spec.get(member['variant']) or {}
        entry={'name':spec.get('name'),'scope':spec.get('scope'),'instructions':variant.get('instructions',''),'files':[]}
        context.append(entry)
        for file in variant.get('files',[]):
            path=workspace/'.foundry-skills'/member['candidate_id']/safe_relative(file['path'])
            path.parent.mkdir(parents=True,exist_ok=True); path.write_text(file['content'],encoding='utf8')
            entry['files'].append(path.relative_to(workspace).as_posix())
    return context

def _tokens(run):
    usage=run.get('usage') or {}
    if usage.get('total_tokens') is not None: return usage['total_tokens']
    vals=[usage.get(k) for k in ('input_tokens','output_tokens')]
    return sum(x for x in vals if isinstance(x,(float,int))) if any(isinstance(x,(float,int)) for x in vals) else None

def _sign_p(wins,losses):
    n=wins+losses
    return sum(math.comb(n,k) for k in range(wins,n+1))/(2**n) if n else 1.0

def _summarize(runs,cases,config,scope):
    pairs=[]
    for c in cases:
        for rep in range(int(config.get('repetitions',3))):
            pair={r['arm']:r for r in runs if r['case_id']==c['id'] and r['repetition']==rep}
            if len(pair)==2: pairs.append(pair)
    infrastructure=[r for r in runs if r.get('status')=='infrastructure_error']
    complete=len(pairs)==len(cases)*int(config.get('repetitions',3))
    valid=[p for p in pairs if all(r.get('status') in ('pass','fail') for r in p.values())]
    baseline=sum(p['baseline']['status']=='pass' for p in valid)/len(valid) if valid else 0
    candidate=sum(p['candidate']['status']=='pass' for p in valid)/len(valid) if valid else 0
    wins=sum(p['baseline']['status']=='fail' and p['candidate']['status']=='pass' for p in valid)
    losses=sum(p['baseline']['status']=='pass' and p['candidate']['status']=='fail' for p in valid)
    quality_p=_sign_p(wins,losses)
    baseline_seconds=sum(p['baseline']['seconds'] for p in valid); candidate_seconds=sum(p['candidate']['seconds'] for p in valid)
    time_ratio=candidate_seconds/baseline_seconds if baseline_seconds else None
    token_pairs=[p for p in valid if all(_tokens(r) is not None for r in p.values())]
    bt=sum(_tokens(p['baseline']) for p in token_pairs); ct=sum(_tokens(p['candidate']) for p in token_pairs)
    token_ratio=ct/bt if bt and len(token_pairs)==len(valid) else None
    turn_pairs=[p for p in valid if all(isinstance(r.get('turns'),int) and not isinstance(r.get('turns'),bool) and r['turns']>=0 for r in p.values())]
    baseline_turns=sum(p['baseline']['turns'] for p in turn_pairs) if valid and len(turn_pairs)==len(valid) else None
    candidate_turns=sum(p['candidate']['turns'] for p in turn_pairs) if baseline_turns is not None else None
    turn_ratio=candidate_turns/baseline_turns if baseline_turns else None
    efficiency_wins=sum(p['candidate']['seconds']<p['baseline']['seconds'] for p in valid)
    efficiency_p=_sign_p(efficiency_wins,len(valid)-efficiency_wins)
    token_wins=sum(_tokens(p['candidate'])<_tokens(p['baseline']) for p in token_pairs)
    token_p=_sign_p(token_wins,len(token_pairs)-token_wins)
    token_efficiency=token_ratio is not None and token_ratio<=1-float(config.get('min_efficiency_gain',.15)) and token_p<=.025
    efficiency=(time_ratio is not None and time_ratio<=1-float(config.get('min_efficiency_gain',.15)) and efficiency_p<=.025) or token_efficiency
    quality=(candidate-baseline>=float(config.get('min_quality_gain',.05)) and quality_p<=.025)
    reasons=[]
    if not complete: reasons.append('evaluación incompleta')
    if infrastructure: reasons.append('errores de infraestructura')
    if not any(c['split']=='origin' for c in cases): reasons.append('falta caso de origen')
    if not any(c['split']=='holdout' for c in cases): reasons.append('falta caso reservado de transferencia')
    if scope=='general' and len({c['domain'] for c in cases})<2: reasons.append('una skill general requiere al menos dos dominios')
    signatures=[digest({'prompt':c['prompt'],'project':json.loads(c['snapshot'])['project_hash'],'verifier':json.loads(c['snapshot'])['verifier_hash'],'verify':c['verify_json']}) for c in cases]
    if len(signatures)!=len(set(signatures)): reasons.append('casos duplicados: el conjunto reservado necesita una tarea distinta')
    per_case=[]
    for case in cases:
        local=[p for p in valid if p['baseline']['case_id']==case['id']]
        before=sum(p['baseline']['seconds'] for p in local); after=sum(p['candidate']['seconds'] for p in local)
        ratio=after/before if before else None
        per_case.append({'case_id':case['id'],'split':case['split'],'pairs':len(local),'baseline_quality':sum(p['baseline']['status']=='pass' for p in local)/len(local) if local else None,'candidate_quality':sum(p['candidate']['status']=='pass' for p in local)/len(local) if local else None,'time_ratio':ratio})
        if ratio is not None and ratio>float(config.get('max_time_ratio',1.25)): reasons.append('límite de tiempo relativo excedido en caso '+case['id'])
        case_losses=sum(p['baseline']['status']=='pass' and p['candidate']['status']=='fail' for p in local)
        per_case[-1]['losses']=case_losses
        # Name the broken case: a regression against a frozen case is a verdict, not a statistic.
        if case_losses: reasons.append('regresión observada en caso '+case['split']+' '+case['id']+' ('+case['title']+'): '+str(case_losses)+' par(es) en que el candidato falla donde la base aprueba')
    if any((p['baseline'].get('provider'),p['baseline'].get('model'))!=(p['candidate'].get('provider'),p['candidate'].get('model')) for p in valid): reasons.append('proveedor o modelo distinto entre brazos pareados')
    if not all(r.get('real_inference') is True for r in runs): reasons.append('driver mecánico o inferencia no acreditada')
    if time_ratio is None or time_ratio>float(config.get('max_time_ratio',1.25)): reasons.append('límite de tiempo relativo excedido')
    if not quality and not (efficiency and baseline==candidate==1): reasons.append('beneficio insuficiente o evidencia estadística insuficiente')
    # Paired-trial verdict (skill-forge change-boundaries): identical outcomes in
    # every pair are "equivalent", never a silent pass or a silent regression.
    if not complete or infrastructure: verdict='inconclusive'
    elif losses: verdict='regressed'
    elif not reasons: verdict='improved'
    elif wins==0 and all(p['baseline']['status']==p['candidate']['status'] for p in valid): verdict='equivalent'
    else: verdict='not_demonstrated'
    return {'eligible':not reasons,'verdict':verdict,'reasons':reasons,'per_case':per_case,'paired_runs':len(pairs),'baseline_quality':baseline,'candidate_quality':candidate,'quality_gain':candidate-baseline,'wins':wins,'losses':losses,'quality_sign_p':quality_p,'efficiency_sign_p':efficiency_p,'token_sign_p':token_p,'time_ratio':time_ratio,'token_ratio':token_ratio,'baseline_turns':baseline_turns,'candidate_turns':candidate_turns,'turn_ratio':turn_ratio,'uncertainty':'Prueba de signos unilateral exacta; repeticiones de una tarea no prueban generalización a tareas nuevas. Umbral p <= 0.025 para dos formulaciones y cero regresiones observadas.','infrastructure_errors':len(infrastructure),'complete':complete}

def _evaluate_candidate(store,candidate_id,job_id):
    from .providers import run_agent
    candidate,spec=_candidate(store,candidate_id)
    episode=store.one('SELECT * FROM episodes WHERE id=?',(candidate['episode_id'],))
    ids=json.loads(episode['cases_json']) if episode else []
    if not ids: raise Invalid('Vincula casos de evaluación a la tarea antes de evaluar.')
    cases=[]
    for id in dict.fromkeys(ids):
        case=store.one('SELECT * FROM cases WHERE id=?',(id,))
        if not case: raise Invalid('Caso de evaluación inexistente: '+id)
        case_paths(store,case); cases.append(case)
    if not any(c['split']=='origin' and _applicable(store,candidate,spec,c) for c in cases):
        raise Invalid('El dominio/alcance no coincide con ningún caso de origen. Revisa el dominio del candidato y sus casos antes de consumir inferencia.')
    config=store.get('config'); repetitions=int(config.get('repetitions',3))
    if repetitions<1 or repetitions>20: raise Invalid('repetitions debe estar entre 1 y 20.')
    max_calls=int(config.get('max_calls',36)); deadline=now()+float(config.get('experiment_seconds',3600))
    base=store.get('active_version'); baseline=_members(store,base); binding=_binding(store,candidate,cases)
    from .readiness import candidate_readiness
    readiness=candidate_readiness(store,candidate_id)
    if not readiness['ready']: raise Invalid('Preparación pendiente: '+' '.join(item['message'] for item in readiness['blockers']))
    provenance=_replay_provenance(store,episode,cases,config)
    order=('preventive',) if episode['label']=='incorrect' else ('positive','preventive')
    variants=[v for v in order if spec.get(v) and (spec[v].get('instructions') or spec[v].get('files'))]
    if not variants or variants[0]!=order[0]: raise Invalid('Falta la formulación requerida por la etiqueta humana: '+order[0])
    id=uid('experiment'); report={'schema':1,'id':id,'candidate_id':candidate_id,'base_version':base,'binding':binding,'cases':[c['id'] for c in cases],'attempts':[],'selected_variant':None,'started':now(),'calls':0,'status':'running','runtime_security':'Subprocesos locales con el usuario actual; sin aislamiento de seguridad. Verificadores congelados con guardas hash.'}
    previous=store.one("SELECT * FROM experiments WHERE candidate_id=? AND base_version=? AND status='running' ORDER BY created DESC LIMIT 1",(candidate_id,base))
    if previous and json.loads(previous['report']).get('binding')==binding:
        report=json.loads(previous['report']); id=previous['id']; report['resumed_at']=now()
        spent=sum(r.get('seconds',0) for a in report['attempts'] for r in a['runs'])
        deadline=now()+max(0,float(config.get('experiment_seconds',3600))-spent)
    else:
        store.execute('INSERT INTO experiments VALUES (?,?,?,?,?,?)',(id,candidate_id,base,js(report),'running',now()))
    report['replay_provenance']=provenance; report['promotion_requires_human']=True
    report['environment_limitations']='Se congelan archivos, instrucciones, configuración y verificadores; los subprocesos comparten usuario, sistema operativo y servicios externos. No se acredita hermeticidad del entorno.'
    store.execute("UPDATE candidates SET status='evaluating' WHERE id=?",(candidate_id,))
    experiment_dir=store.root/'runs'/id; experiment_dir.mkdir(exist_ok=True)
    def cancelled():
        if (job_id and store.cancelled(job_id)) or now()>deadline: return True
        current=store.task_completion(episode['id'])
        return not current['completed'] or current['completion_generation']!=binding['completion']['completion_generation']
    def checkpoint():
        write_json(experiment_dir/'report.json',report)
        store.execute('UPDATE experiments SET report=?,status=? WHERE id=?',(js(report),report['status'],id))
    budget_hit=False
    for variant in variants:
        attempt=next((a for a in report['attempts'] if a['variant']==variant),None)
        if attempt is None:
            attempt={'variant':variant,'runs':[],'summary':None}; report['attempts'].append(attempt)
        if attempt.get('summary') and attempt['summary']['eligible']:
            report['selected_variant']=variant; break
        if attempt.get('summary') and attempt['summary']['complete'] and not attempt['summary']['infrastructure_errors']: continue
        replacements=set(spec.get('replaces',[]))
        candidate_members=[m for m in baseline if m['candidate_id'] not in replacements]+[{'candidate_id':candidate_id,'variant':variant}]
        stop=False
        for case in cases:
            for rep in range(repetitions):
                arms=['baseline','candidate']; random.SystemRandom().shuffle(arms)
                for arm in arms:
                    if any(r['case_id']==case['id'] and r['repetition']==rep and r['arm']==arm for r in attempt['runs']): continue
                    if cancelled() or report['calls']>=max_calls:
                        budget_hit=True; stop=True; break
                    store.progress(job_id,f'{variant}: {case["title"]}, repetición {rep+1}/{repetitions}, {arm}') if job_id else None
                    source,verifier=case_paths(store,case)
                    run={'case_id':case['id'],'repetition':rep,'arm':arm,'real_inference':False,'seconds':0,'usage':{},'status':'infrastructure_error'}
                    with tempfile.TemporaryDirectory(prefix='replay-',dir=experiment_dir) as td:
                        workspace=Path(td)/'workspace'; shutil.copytree(source,workspace)
                        context=_apply(store,baseline if arm=='baseline' else candidate_members,case,workspace)
                        prompt=('Complete the task in this isolated replay workspace. Respect existing project instructions. '\
                            'Only modify this workspace. Do not inspect parent directories or evaluator internals. '\
                            'Additional evaluated harness capabilities (files are under .foundry-skills):\n'+js(context)+'\n\nUSER TASK:\n'+case['prompt'])
                        started=now()
                        try:
                            if cancelled(): raise Invalid('Evaluación cancelada o presupuesto de tiempo agotado.')
                            provider_config=dict(config['replay']); provider_config['timeout']=min(float(provider_config.get('timeout',180)),max(.01,deadline-now()))
                            response=run_agent(provider_config,prompt,workspace,cancel=cancelled)
                            run.update({k:response.get(k) for k in ('usage','provider','model','real_inference','turns','tool_calls')})
                            run['answer']=str(response.get('text',''))[:16000]
                            # Validate integrity before AND after verification. Never execute a changed evaluator.
                            case_paths(store,case)
                            verdict=verifier_run(json.loads(case['verify_json']),workspace,verifier,timeout=min(60,max(.01,deadline-now())),cancel=cancelled)
                            case_paths(store,case)
                            run['verification']=verdict; run['status']=verdict['status']
                        except Exception as exc: run['error']=str(exc)[:4000]
                        run['seconds']=now()-started
                    report['calls']+=1; attempt['runs'].append(run); checkpoint()
                    if run['status']=='infrastructure_error': stop=True; break
                if stop: break
            if stop: break
        attempt['summary']=_summarize(attempt['runs'],cases,config,spec.get('scope'))
        checkpoint()
        if cancelled() or budget_hit: break
        if attempt['summary']['eligible']:
            report['selected_variant']=variant; break
        if attempt['summary']['infrastructure_errors']: break
    if job_id and store.cancelled(job_id): report['status']='cancelled'
    elif budget_hit or now()>deadline or any(a['summary']['infrastructure_errors'] for a in report['attempts']): report['status']='inconclusive'
    elif report['selected_variant']: report['status']='passed'
    else: report['status']='discarded'
    report['finished']=now()
    # Cancellation and provenance checks share the transaction with final persistence.
    with store.transaction():
        if job_id and store.cancelled(job_id): report['status']='cancelled'; report['selected_variant']=None
        fresh,_=_candidate(store,candidate_id)
        try: stale=_binding(store,fresh,cases)!=binding or store.get('active_version')!=base
        except Invalid: stale=True
        if stale:
            report['status']='stale'; report['selected_variant']=None
        checkpoint()
        state='evaluated' if report['status']=='passed' else ('discarded' if report['status']=='discarded' else 'inconclusive')
        store.execute('UPDATE candidates SET status=? WHERE id=?',(state,candidate_id))
        store.audit('evaluation.finished',{'experiment_id':id,'status':report['status']})
    return report


def evaluate_candidate(store,candidate_id,job_id):
    """Keep fatal setup/integrity failures visible and never leave false running state.

    BaseException is deliberately not swallowed: process interruption keeps the
    last checkpoint resumable. Ordinary failures finalize an honest report.
    """
    try: return _evaluate_candidate(store,candidate_id,job_id)
    except Exception as exc:
        row=store.one("SELECT * FROM experiments WHERE candidate_id=? AND status='running' ORDER BY created DESC LIMIT 1",(candidate_id,))
        if not row: raise
        report=json.loads(row['report'])
        report.update(status='cancelled' if job_id and store.cancelled(job_id) else 'inconclusive',selected_variant=None,finished=now(),fatal_error=clean(str(exc))[:4000])
        with store.transaction():
            store.execute('UPDATE experiments SET report=?,status=? WHERE id=?',(js(report),report['status'],row['id']))
            store.execute("UPDATE candidates SET status='inconclusive' WHERE id=?",(candidate_id,))
            store.audit('evaluation.failed',{'experiment_id':row['id'],'reason':report['fatal_error']})
        write_json(store.root/'runs'/row['id']/'report.json',report)
        return report

def promote(store,candidate_id,experiment_id,comment=''):
    from .miner import risk_acknowledged, record_decision
    with store.transaction():
        candidate,spec=_candidate(store,candidate_id)
        if not risk_acknowledged(store,candidate_id,spec): raise Invalid('El lint de riesgo marcó este candidato ('+', '.join(spec.get('risk') or [])+'); un humano debe reconocer los hallazgos antes de adoptarlo.')
        exp=store.one('SELECT * FROM experiments WHERE id=? AND candidate_id=?',(experiment_id,candidate_id))
        if not exp: raise Invalid('Experimento inexistente.')
        report=json.loads(exp['report'])
        if exp['status']!='passed' or not report.get('selected_variant'): raise Invalid('El experimento no demostró una mejora promovible.')
        base=store.get('active_version')
        if base!=exp['base_version']: raise Invalid('El arnés activo cambió. Reevalúa el candidato.')
        cases=[store.one('SELECT * FROM cases WHERE id=?',(id,)) for id in report['cases']]
        if any(c is None for c in cases) or _binding(store,candidate,cases)!=report['binding']: raise Invalid('Cambió la evidencia, etiqueta, configuración o candidato. Reevalúa.')
        episode=store.one('SELECT * FROM episodes WHERE id=?',(candidate['episode_id'],))
        provenance=_replay_provenance(store,episode,cases,store.get('config'))
        if provenance!=report.get('replay_provenance') or not provenance['promotion_eligible']: raise Invalid('La promoción exige un snapshot inicial vinculado a la tarea. Un benchmark reconstruido no acredita el origen histórico.')
        if episode['label']=='incorrect' and report['selected_variant']!='preventive': raise Invalid('Una tarea incorrecta sólo permite promover la formulación preventiva evaluada.')
        for case in cases: case_paths(store,case)
        selected=next(a for a in report['attempts'] if a['variant']==report['selected_variant'])
        if not _summarize(selected['runs'],cases,store.get('config'),spec.get('scope'))['eligible']: raise Invalid('El experimento no supera los criterios actuales.')
        members=_members(store,base); replaces=set(spec.get('replaces',[])); active={m['candidate_id'] for m in members}
        if not replaces.issubset(active): raise Invalid('Una sustitución apunta a una capacidad que ya no está activa.')
        for member in members:
            _,old=_candidate(store,member['candidate_id'])
            if member['candidate_id'] in replaces and (old.get('scope'),old.get('domain'))!=(spec.get('scope'),spec.get('domain')): raise Invalid('Una sustitución debe conservar el alcance de la capacidad previa.')
            if member['candidate_id'] not in replaces and (old.get('name'),old.get('kind'),old.get('scope'),old.get('domain'))==(spec.get('name'),spec.get('kind'),spec.get('scope'),spec.get('domain')): raise Invalid('Ya existe una capacidad equivalente; propone una sustitución evaluada.')
        members=[m for m in members if m['candidate_id'] not in replaces]+[{'candidate_id':candidate_id,'variant':report['selected_variant']}]
        id=uid('version'); folder=store.root/'registry'/id; folder.mkdir()
        package={'schema':1,'id':id,'parent':base,'members':members,'experiment_id':experiment_id,'artifacts':[]}
        for member in members:
            _,member_spec=_candidate(store,member['candidate_id']); content=member_spec[member['variant']]
            package['artifacts'].append({'candidate_id':member['candidate_id'],'variant':member['variant'],'name':member_spec['name'],'kind':member_spec['kind'],'scope':member_spec['scope'],'domain':member_spec.get('domain'),'content':content})
            for file in content.get('files',[]):
                p=folder/'artifacts'/member['candidate_id']/safe_relative(file['path']); p.parent.mkdir(parents=True,exist_ok=True); p.write_text(file['content'],encoding='utf8')
        write_json(folder/'manifest.json',package)
        store.execute('INSERT INTO versions VALUES (?,?,?,?,?)',(id,base,js(members),now(),'Validated experiment '+experiment_id))
        store.set('active_version',id); store.execute("UPDATE candidates SET status='accepted' WHERE id=?",(candidate_id,)); store.audit('registry.promoted',{'version':id,'experiment_id':experiment_id,'package_hash':digest(package)})
        decision=record_decision(store,candidate_id,'adopt',comment,{'version':id,'experiment_id':experiment_id})
    return {'id':id,'parent':base,'members':members,'path':str(folder),'decision':decision}

def rollback(store,version):
    with store.transaction():
        members=_members(store,version); previous=store.get('active_version')
        store.set('active_version',version); store.audit('registry.rollback',{'from':previous,'to':version})
    return {'id':version,'previous':previous,'members':members}
