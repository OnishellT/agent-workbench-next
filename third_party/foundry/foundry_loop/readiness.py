"""Read-only evaluation preflight and explicit case-selection helpers.

Readiness is configuration validation, never an experiment or a claim of gain.
No inference, control execution, origin attestation or human labeling occurs here.
"""
from __future__ import annotations
import json
from pathlib import Path
from .common import Invalid, digest, js


def source_directories(store, revision):
    # Filter inside SQLite instead of deserializing a potentially huge transcript
    # merely to render the setup panel. Never recover redacted values from raw files.
    rows=store.rows("SELECT json_extract(j.value,'$.metadata.cwd') AS cwd FROM revisions r, json_each(r.events) j WHERE r.id=? AND json_extract(j.value,'$.kind')='session_metadata'",(revision,))
    return sorted({r['cwd'] for r in rows if isinstance(r['cwd'],str) and Path(r['cwd']).is_absolute() and '[REDACTED' not in r['cwd']})


def candidate_readiness(store,candidate_id,deep=False):
    from .evaluate import _candidate, _binding, _applicable, _replay_provenance
    from .snapshots import case_paths
    from .providers import diagnostics
    candidate,spec=_candidate(store,candidate_id)
    episode=store.one('SELECT * FROM episodes WHERE id=?',(candidate['episode_id'],))
    if not episode: raise Invalid('La tarea fuente del candidato ya no existe.')
    config=store.get('config'); blockers=[]; warnings=[]
    def add(target,code,message,action): target.append({'code':code,'message':message,'action':action})
    if not store.task_completed(episode['id']): add(blockers,'task_incomplete','Confirma la finalización de la tarea antes de evaluar.','complete_task')
    if episode['label'] not in ('correct','incorrect'): add(blockers,'task_unlabeled','Marca la tarea como correcta o incorrecta.','label_task')
    coverage=store.get('revision_coverage:'+episode['revision'])
    if coverage is None:
        session=store.one('SELECT revision,coverage FROM sessions WHERE id=?',(episode['session_id'],))
        if session and session['revision']==episode['revision']: coverage=json.loads(session['coverage'])
    if not coverage or coverage.get('status')!='complete': add(blockers,'source_incomplete','La captura fuente está incompleta o su cobertura no está acreditada. Corrige la captura y genera una propuesta con evidencia completa.','derive')
    completion=store.task_completion(episode['id'])
    episode_hash=digest({k:episode[k] for k in ('revision','label','feedback','first_event','last_event')})
    if spec.get('completion_generation')!=completion['completion_generation'] or (spec.get('episode_binding') and spec['episode_binding']!=episode_hash):
        add(blockers,'candidate_stale','La finalización, etiqueta o evidencia cambió desde la derivación. Genera de nuevo el candidato.','derive')
    ids=json.loads(episode['cases_json']); cases=[]
    for id in dict.fromkeys(ids):
        case=store.one('SELECT * FROM cases WHERE id=?',(id,))
        if not case: add(blockers,'case_missing','El caso seleccionado ya no existe: '+str(id),'select_cases'); continue
        cases.append(case)
        try:
            manifest=json.loads(case['snapshot'])
            if not all(manifest.get(k) for k in ('project_key','project_hash','verifier_hash','directory')): raise Invalid('Manifest incompleto.')
            for key in ('verify_json','negative_json'):
                command=json.loads(case[key])
                if not isinstance(command,list) or not command or not all(isinstance(x,str) and x for x in command): raise Invalid('Comando inválido.')
            if deep: case_paths(store,case)
        except (Invalid,ValueError,KeyError) as exc:
            add(blockers,'snapshot_invalid','Recrea el snapshot/verificador de '+case['title']+': '+str(exc),'repair_snapshot')
    from .miner import risk_acknowledged
    if not risk_acknowledged(store,candidate_id,spec): add(blockers,'risk_unacknowledged','El lint de riesgo marcó patrones en las instrucciones ('+', '.join(spec['risk'])+'). Revísalos y reconócelos expresamente antes de evaluar o adoptar.','acknowledge_risk')
    if not any(c['split']=='origin' for c in cases): add(blockers,'missing_origin','Crea o selecciona un caso de origen: estado inicial del proyecto, instrucciones y un verificador observable con controles positivo y negativo.','create_origin')
    if not any(c['split']=='holdout' for c in cases): add(blockers,'missing_holdout','Añade un caso reservado distinto que compruebe transferencia; repetir el caso de origen no sirve como caso reservado.','create_holdout')
    if cases and not any(b['code']=='snapshot_invalid' for b in blockers) and not any(c['split']=='origin' and _applicable(store,candidate,spec,c) for c in cases): add(blockers,'scope_mismatch','El dominio o alcance no coincide con los casos de origen seleccionados.','edit_scope')
    if any(c['split']=='holdout' for c in cases) and not any(b['code']=='snapshot_invalid' for b in blockers) and not any(c['split']=='holdout' and _applicable(store,candidate,spec,c) for c in cases):
        add(blockers,'holdout_scope_mismatch','La capacidad no se aplicaría a ningún caso reservado. Selecciona un escenario de transferencia dentro de su dominio y alcance.','select_cases')
    if spec.get('scope')=='general' and len({c['domain'] for c in cases})<2: add(blockers,'missing_domains','Una capacidad general requiere casos en al menos dos dominios.','select_cases')
    if cases and not any(b['code']=='snapshot_invalid' for b in blockers):
        signatures=[digest({'prompt':c['prompt'],'project':json.loads(c['snapshot'])['project_hash'],'verifier':json.loads(c['snapshot'])['verifier_hash'],'verify':c['verify_json']}) for c in cases]
        if len(signatures)!=len(set(signatures)): add(blockers,'duplicate_cases','Hay casos idénticos. El escenario reservado necesita una tarea o verificación diferente.','select_cases')
    provider=diagnostics(config)['providers']['replay']
    if not provider['ready']: add(blockers,'provider_unavailable','No se encuentra el ejecutable del proveedor de evaluación. Revisa su configuración.','settings')
    reps=int(config['repetitions']); pairs=len(cases)*reps; variants=1 if episode['label']=='incorrect' else 2
    first=pairs*2; all_calls=first*variants
    if first>int(config['max_calls']): add(blockers,'insufficient_budget',f'La primera comparación requiere {first} llamadas; max_calls permite {config["max_calls"]}. Ajusta el presupuesto.','settings')
    elif all_calls>int(config['max_calls']): add(warnings,'fallback_budget',f'Probar ambas formulaciones puede necesitar {all_calls} llamadas; el presupuesto actual sólo cubre parte del fallback.','settings')
    if pairs<6: add(warnings,'insufficient_pairs','Con este número de pares no puede alcanzarse p ≤ 0.025 ni siquiera ganando todos. Aumenta repeticiones o usa el resultado sólo como diagnóstico.','settings')
    if config['replay']['kind']=='command' and config['replay'].get('real_inference') is not True:
        add(warnings,'mechanical_driver','El driver está configurado como mecánico: sirve para validar el protocolo, pero no acredita mejora de un modelo.','settings')
    provenance=None
    if cases and not any(b['code'] in ('snapshot_invalid','case_missing') for b in blockers):
        try: provenance=_replay_provenance(store,episode,cases,config)
        except Invalid as exc: add(blockers,'origin_mismatch',str(exc),'configure_origin')
    if not store.get('episode_origin:'+episode['id']): add(warnings,'origin_unverified','Puedes medir un benchmark reconstruido, pero para adoptar necesitas vincular un snapshot inicial real; no se infiere de la conversación.','configure_origin')
    if not any(b['code'] in ('task_incomplete','task_unlabeled','candidate_stale') for b in blockers):
        try: _binding(store,candidate,cases)
        except Invalid as exc: add(blockers,'evidence_invalid',str(exc),'derive')
    dirs=source_directories(store,episode['revision'])
    available=[]
    for row in store.rows('SELECT id,title,domain,split,snapshot FROM cases ORDER BY created DESC'):
        if row['domain']!=spec.get('domain') and spec.get('scope')!='general': continue
        available.append({k:row[k] for k in ('id','title','domain','split')}|{'compatibility':'domain_match_requires_review' if spec.get('scope')!='general' else 'general_scope_requires_review'})
    return {'candidate_id':candidate_id,'episode_id':episode['id'],'episode':store.enrich_episode(episode),'ready':not blockers,'blockers':blockers,'warnings':warnings,
            'selected_cases':[{k:c[k] for k in ('id','title','domain','split')} for c in cases],'available_cases':available,
            'setup':{'domain':spec.get('domain'),'scope':spec.get('scope'),'expected_revision':episode['revision'],'project_path':dirs[0] if len(dirs)==1 else None,
                     'required_splits':['origin','holdout'],'required_case_fields':['project_path','title','prompt','domain','split','verify','negative_control'],
                     'positive_control':'Necesario si el snapshot inicial todavía no satisface el verificador.','source_project_known':len(dirs)==1},
            'budget':{'repetitions':reps,'pairs_per_variant':pairs,'calls_first_variant':first,'calls_all_variants':all_calls,'max_calls':config['max_calls']},
            'provider':provider,'promotion':{'requires_human':True,'origin_linked':bool(provenance and provenance['promotion_eligible']),
                'possible_with_current_design':not blockers and pairs>=6 and bool(provenance and provenance['promotion_eligible']) and not any(w['code']=='mechanical_driver' for w in warnings)},
            'replay_provenance':provenance,'integrity_checked':deep}


def select_candidate_cases(store,candidate_id,case_ids):
    if not isinstance(case_ids,list) or any(not isinstance(id,str) for id in case_ids): raise Invalid('case_ids debe ser una lista de identificadores.')
    with store.transaction():
        candidate=store.one('SELECT * FROM candidates WHERE id=?',(candidate_id,))
        if not candidate: raise Invalid('Candidato desconocido.')
        if store.one("SELECT id FROM jobs WHERE kind='evaluate' AND subject=? AND status IN ('queued','running')",(candidate_id,)):
            raise Invalid('Cancela la evaluación en curso antes de cambiar sus casos.')
        if any(not store.one('SELECT id FROM cases WHERE id=?',(id,)) for id in case_ids): raise Invalid('Caso desconocido.')
        store.execute('UPDATE episodes SET cases_json=? WHERE id=?',(js(list(dict.fromkeys(case_ids))),candidate['episode_id']))
        store.audit('candidate.cases_selected',{'candidate_id':candidate_id,'episode_id':candidate['episode_id'],'case_ids':list(dict.fromkeys(case_ids))})
    return candidate_readiness(store,candidate_id)
